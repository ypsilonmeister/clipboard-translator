package settings

import (
	"os/exec"
	"testing"
)

func TestLanguageOnlyRendering(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable for page rendering check")
	}
	cmd := exec.Command(node, "-e", `
const fs=require('fs'), assert=require('node:assert/strict');
const js=fs.readFileSync('web/app.js','utf8');
const start=js.indexOf('  function langSelect('), end=js.indexOf('  function showFieldError(',start);
assert(start>=0 && end>start);
function h(tag,attrs,...children) {
 const el={tag,value:'',...attrs,children:children.flat(),append(...items){this.children.push(...items)},replaceChildren(...items){this.children=items}};
 return el;
}
const elements={}; const $=id=>elements[id] ||= h('div',{});
const HOTKEY_KEYS=['E','T']; const appBusy=()=>false;
let touched=false; const touchField=()=>{touched=true};
let state,model;
eval(js.slice(start,end));
function all(el){return [el,...(el.children||[]).filter(x=>typeof x==='object').flatMap(all)]}
for (const languageOnly of [false,true,false]) {
 state={language_only:languageOnly,max_hotkeys:4,win_key:'Super',language_options:[{code:'ja',name:'Japanese'},{code:'en',name:'English'},{code:'de',name:'German'}]};
 model={profiles:[{key:'E',mode:'pair',a:'ja',b:'en',target:'en'},{key:'T',mode:'target',target:'de'}]};
 renderHotkeys();
 assert.equal($('hotkeys-title').textContent,languageOnly?'Languages':'Hotkeys');
 assert.equal($('profiles').children.length,languageOnly?1:2);
 assert.equal($('add').hidden,languageOnly);
 const nodes=all($('profiles'));
 const labels=nodes.map(n=>n.text||n['aria-label']||'').join(' ');
 for(const text of ['Text from','Output','Line breaks','Keys','Remove hotkey']) assert.equal(labels.includes(text),!languageOnly,text);
 if(languageOnly){
  assert(!labels.includes('hotkey'));
  const select=nodes.find(n=>n['aria-label']==='First language');select.onchange({target:{value:'de'}});
  assert.equal(model.profiles[0].a,'de');assert(touched);
  const targetRadio=nodes.find(n=>n.tag==='input'&&n.value==='target');targetRadio.onchange();
  assert.equal(model.profiles[0].mode,'target');
  const target=all($('profiles')).find(n=>n['aria-label']==='Target language');
  target.onchange({target:{value:'ja'}});assert.equal(model.profiles[0].target,'ja');
  assert.equal(model.profiles[1].target,'de');
 }
}
`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("render languages: %v\n%s", err, out)
	}
}
