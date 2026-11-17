package app

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
)

func (a *app) loadStartupConfig() (config.Config, bool) {
	c, err := config.Load(a.dir)
	if err == nil {
		return c, true
	}
	log.Printf("config: %v", err)
	path, _ := filepath.Abs(filepath.Join(a.dir, "config.json"))
	diagnostic := "Could not load settings:\n\n" + err.Error()
	if _, backupErr := config.LoadFile(path + ".bak"); backupErr == nil {
		if a.askYesNo(diagnostic + "\n\nStart with the previous settings?\n\nThe broken file will be kept separately before restoring " + path + ".bak.") {
			if restoreErr := restoreConfigBackup(path); restoreErr != nil {
				log.Printf("config recovery: %v", restoreErr)
				a.showErr("Could not restore " + path + ": " + restoreErr.Error())
				return c, false
			}
			c, err = config.Load(a.dir)
			if err != nil {
				a.showErr(err.Error())
				return c, false
			}
			return c, true
		}
	}
	if a.askYesNo(diagnostic + "\n\nOpen config.json in your editor?\n\nThe app will exit. Save your corrections and start the app again.") {
		if err := a.openConfigEditor(path); err != nil {
			log.Printf("could not open config.json: %v", err)
			a.showErr("Could not open config.json (" + path + "). Open it manually in any text editor, save your corrections, and restart the app.")
		}
	}
	return c, false
}

func restoreConfigBackup(path string) error {
	// Recheck after the dialog in case the backup was edited while it was open.
	if _, err := config.LoadFile(path + ".bak"); err != nil {
		return err
	}
	previous, err := os.ReadFile(path + ".bak")
	if err != nil {
		return err
	}
	broken, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Never overwrite a previous recovery copy. Finish and flush the copy before
	// touching config.json, even when restoration itself subsequently fails.
	name := path + ".broken"
	var saved *os.File
	for i := 1; ; i++ {
		saved, err = os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if !errors.Is(err, os.ErrExist) {
			break
		}
		name = fmt.Sprintf("%s.broken.%d", path, i)
	}
	if err != nil {
		return err
	}
	_, err = saved.Write(broken)
	if err == nil {
		err = saved.Sync()
	}
	closeErr := saved.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	log.Printf("saved broken config as %s; restoring %s.bak", name, path)
	// In-place writes preserve Scoop's persist hard link. Do not use UpdateFile:
	// it would overwrite the good .bak with the broken settings.
	return os.WriteFile(path, previous, 0o644)
}
