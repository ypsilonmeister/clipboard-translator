// Command mksyso writes a Windows resource object (resource_windows_amd64.syso)
// holding the application icon, version information and manifest. The Go
// linker picks the .syso up automatically when building for windows/amd64, so
// ClipboardTranslator.exe gets its icon, the Details tab in Explorer and
// per-monitor DPI awareness.
//
// It uses only the standard library. Run from the repository root:
//
//	go run ./tools/mksyso -version 0.4.1
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtVersion   = 16
	rtManifest  = 24
	langEnUS    = 0x0409
)

type resource struct {
	typ, id uint32
	data    []byte
}

func main() {
	version := flag.String("version", "0.0.0", "product version, e.g. 0.4.1 (a leading v and a -suffix are ignored for the numeric part)")
	iconPath := flag.String("icon", "assets/icon.ico", "multi-size .ico file")
	manifestPath := flag.String("manifest", "assets/app.manifest", "application manifest (DPI awareness, execution level)")
	out := flag.String("o", "resource_windows_amd64.syso", "output object file")
	flag.Parse()

	ico, err := os.ReadFile(*iconPath)
	if err != nil {
		log.Fatal(err)
	}
	res, err := iconResources(ico)
	if err != nil {
		log.Fatal(err)
	}
	vi, err := versionResource(strings.TrimPrefix(*version, "v"))
	if err != nil {
		log.Fatal(err)
	}
	manifest, err := os.ReadFile(*manifestPath)
	if err != nil {
		log.Fatal(err)
	}
	res = append(res, vi, resource{rtManifest, 1, manifest})
	if err := os.WriteFile(*out, buildObject(res), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s (version %s, %d resources)", *out, *version, len(res))
}

// iconResources splits an .ico into RT_ICON images plus one RT_GROUP_ICON that
// lists them. The group has ID 1 so it is the executable's icon in Explorer;
// the tray code loads the same ID.
func iconResources(ico []byte) ([]resource, error) {
	le := binary.LittleEndian
	if len(ico) < 6 || le.Uint16(ico[0:]) != 0 || le.Uint16(ico[2:]) != 1 {
		return nil, fmt.Errorf("not an .ico file")
	}
	n := int(le.Uint16(ico[4:]))
	var res []resource
	grp := new(bytes.Buffer)
	binary.Write(grp, le, [3]uint16{0, 1, uint16(n)})
	for i := 0; i < n; i++ {
		e := ico[6+16*i : 6+16*(i+1)]
		size, off := int(le.Uint32(e[8:])), int(le.Uint32(e[12:]))
		if off+size > len(ico) {
			return nil, fmt.Errorf("icon entry %d is out of range", i)
		}
		id := uint32(i + 1)
		res = append(res, resource{rtIcon, id, ico[off : off+size]})
		// GRPICONDIRENTRY: same as the file entry, but the last field is the RT_ICON id.
		grp.Write(e[:12])
		binary.Write(grp, le, uint16(id))
	}
	return append(res, resource{rtGroupIcon, 1, grp.Bytes()}), nil
}

func versionResource(v string) (resource, error) {
	num := v
	if i := strings.IndexAny(num, "-+"); i >= 0 {
		num = num[:i]
	}
	parts := strings.Split(num, ".")
	var n [4]uint16
	if len(parts) < 1 || len(parts) > 4 {
		return resource{}, fmt.Errorf("bad version %q", v)
	}
	for i, p := range parts {
		x, err := strconv.ParseUint(p, 10, 16)
		if err != nil {
			return resource{}, fmt.Errorf("bad version %q: %v", v, err)
		}
		n[i] = uint16(x)
	}
	le := binary.LittleEndian

	// VS_FIXEDFILEINFO
	ffi := new(bytes.Buffer)
	for _, x := range []uint32{
		0xFEEF04BD, 0x00010000,
		uint32(n[0])<<16 | uint32(n[1]), uint32(n[2])<<16 | uint32(n[3]), // file version
		uint32(n[0])<<16 | uint32(n[1]), uint32(n[2])<<16 | uint32(n[3]), // product version
		0x3F, 0, 0x00040004, 1, 0, 0, 0, // mask, flags, VOS_NT_WINDOWS32, VFT_APP, subtype, date
	} {
		binary.Write(ffi, le, x)
	}

	fileVer := fmt.Sprintf("%d.%d.%d.%d", n[0], n[1], n[2], n[3])
	strs := [][2]string{
		{"CompanyName", "ypsilonmeister"},
		{"FileDescription", "Clipboard Translator"},
		{"FileVersion", fileVer},
		{"InternalName", "ClipboardTranslator"},
		{"LegalCopyright", "Copyright (c) 2026 ypsilonmeister. MIT License."},
		{"OriginalFilename", "ClipboardTranslator.exe"},
		{"ProductName", "Clipboard Translator"},
		{"ProductVersion", v},
		{"Comments", "https://github.com/ypsilonmeister/clipboard-translator"},
	}
	var table []byte
	for _, s := range strs {
		val := utf16Z(s[1])
		table = append(table, node(s[0], val, uint16(len(val)/2), 1, nil)...)
	}
	stringFileInfo := node("StringFileInfo", nil, 0, 1, node("040904B0", nil, 0, 1, table))
	translation := make([]byte, 4)
	le.PutUint32(translation, 0x04B00409) // LANG_ENGLISH_US, Unicode codepage
	varFileInfo := node("VarFileInfo", nil, 0, 1, node("Translation", translation, 4, 0, nil))

	children := append(stringFileInfo, varFileInfo...)
	return resource{rtVersion, 1, node("VS_VERSION_INFO", ffi.Bytes(), uint16(ffi.Len()), 0, children)}, nil
}

func utf16Z(s string) []byte {
	u := utf16.Encode([]rune(s))
	u = append(u, 0)
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return b
}

func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

// node encodes one VS_VERSIONINFO-style block: length, value length, type,
// key, then the value and the children, each DWORD aligned. The returned slice
// is padded so siblings can be concatenated.
func node(key string, value []byte, valueLen, typ uint16, children []byte) []byte {
	var b []byte
	b = append(b, 0, 0) // wLength, patched below
	b = binary.LittleEndian.AppendUint16(b, valueLen)
	b = binary.LittleEndian.AppendUint16(b, typ)
	b = append(b, utf16Z(key)...)
	b = pad4(b)
	b = append(b, value...)
	b = pad4(b)
	b = append(b, children...)
	binary.LittleEndian.PutUint16(b, uint16(len(b)))
	return pad4(b)
}

// buildObject lays the resources out as a PE resource tree (type -> id ->
// language -> data) inside a single .rsrc section of an AMD64 COFF object.
// Every data entry's RVA is a relocation against the section symbol.
func buildObject(res []resource) []byte {
	le := binary.LittleEndian
	sort.Slice(res, func(i, j int) bool {
		if res[i].typ != res[j].typ {
			return res[i].typ < res[j].typ
		}
		return res[i].id < res[j].id
	})
	var types []uint32
	byType := map[uint32][]int{}
	for i, r := range res {
		if len(byType[r.typ]) == 0 {
			types = append(types, r.typ)
		}
		byType[r.typ] = append(byType[r.typ], i)
	}

	// Offsets of each table, in emission order.
	off := 16 + 8*len(types)
	typeDir := map[uint32]int{}
	for _, t := range types {
		typeDir[t] = off
		off += 16 + 8*len(byType[t])
	}
	langDir := make([]int, len(res))
	for _, t := range types {
		for _, i := range byType[t] {
			langDir[i] = off
			off += 16 + 8
		}
	}
	entry := make([]int, len(res))
	for i := range res {
		entry[i] = off
		off += 16
	}
	blob := make([]int, len(res))
	for i, r := range res {
		blob[i] = off
		off += (len(r.data) + 3) &^ 3
	}
	rawSize := off

	raw := new(bytes.Buffer)
	dir := func(entries int) {
		binary.Write(raw, le, [3]uint32{0, 0, 0}) // characteristics, timestamp, version
		binary.Write(raw, le, [2]uint16{0, uint16(entries)})
	}
	dir(len(types))
	for _, t := range types {
		binary.Write(raw, le, [2]uint32{t, 0x80000000 | uint32(typeDir[t])})
	}
	for _, t := range types {
		dir(len(byType[t]))
		for _, i := range byType[t] {
			binary.Write(raw, le, [2]uint32{res[i].id, 0x80000000 | uint32(langDir[i])})
		}
	}
	for _, t := range types {
		for _, i := range byType[t] {
			dir(1)
			binary.Write(raw, le, [2]uint32{langEnUS, uint32(entry[i])})
		}
	}
	for i, r := range res {
		binary.Write(raw, le, [4]uint32{uint32(blob[i]), uint32(len(r.data)), 0, 0})
	}
	for _, r := range res {
		raw.Write(pad4(append([]byte(nil), r.data...)))
	}
	if raw.Len() != rawSize {
		log.Fatalf("internal error: resource layout is %d bytes, expected %d", raw.Len(), rawSize)
	}

	const headers = 20 + 40
	relocAt := headers + rawSize
	symAt := relocAt + 10*len(res)

	out := new(bytes.Buffer)
	// COFF file header.
	binary.Write(out, le, uint16(0x8664)) // IMAGE_FILE_MACHINE_AMD64
	binary.Write(out, le, uint16(1))      // sections
	binary.Write(out, le, uint32(0))      // timestamp
	binary.Write(out, le, uint32(symAt))
	binary.Write(out, le, uint32(1)) // symbols
	binary.Write(out, le, uint16(0)) // optional header
	binary.Write(out, le, uint16(0)) // characteristics
	// Section header.
	out.Write([]byte(".rsrc\x00\x00\x00"))
	binary.Write(out, le, [4]uint32{0, 0, uint32(rawSize), headers})
	binary.Write(out, le, [2]uint32{uint32(relocAt), 0})
	binary.Write(out, le, [2]uint16{uint16(len(res)), 0})
	binary.Write(out, le, uint32(0x40300040)) // initialized data, 4-byte aligned, readable
	out.Write(raw.Bytes())
	// Relocations: IMAGE_REL_AMD64_ADDR32NB against symbol 0 for each data RVA.
	for i := range res {
		binary.Write(out, le, uint32(entry[i]))
		binary.Write(out, le, uint32(0))
		binary.Write(out, le, uint16(3))
	}
	// Symbol table: one static symbol for the section, then an empty string table.
	out.Write([]byte(".rsrc\x00\x00\x00"))
	binary.Write(out, le, uint32(0)) // value
	binary.Write(out, le, uint16(1)) // section number
	binary.Write(out, le, uint16(0)) // type
	out.Write([]byte{3, 0})          // IMAGE_SYM_CLASS_STATIC, no aux symbols
	binary.Write(out, le, uint32(4)) // string table size
	return out.Bytes()
}
