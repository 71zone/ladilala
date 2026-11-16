package inject

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/8bu/ladilala/core/profile"
)

func TestScriptDeterministicAndCoherent(t *testing.T) {
	for _, platform := range []string{profile.PlatformWindows, profile.PlatformMacOS, profile.PlatformLinux} {
		fp := profile.NewFingerprint(7, profile.Options{Platform: platform, Language: "de-DE"})
		if issues := profile.Validate(fp); len(issues) > 0 {
			t.Fatalf("%s: fingerprint incoherent: %v", platform, issues)
		}
		a, err := ScriptFor(fp)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := ScriptFor(fp)
		if a != b {
			t.Fatalf("%s: same fingerprint gave different scripts", platform)
		}
		other, _ := ScriptFor(profile.NewFingerprint(8, profile.Options{Platform: platform, Language: "de-DE"}))
		if a == other {
			t.Fatalf("%s: different seeds gave the same script", platform)
		}
		if strings.Contains(a, placeholder) {
			t.Fatal("placeholder left in script")
		}
		cfg := NewConfig(fp)
		if cfg.HardwareThreads != fp.HardwareThreads || cfg.GPURenderer != fp.GPURenderer || cfg.Languages[0] != "de-DE" || cfg.Languages[1] != "de" {
			t.Fatalf("%s: config does not follow the fingerprint: %+v", platform, cfg)
		}
	}
}

func TestRealModeLeavesSurfaceAlone(t *testing.T) {
	fp := profile.NewFingerprint(1, profile.Options{})
	fp.Canvas, fp.WebGL = profile.ModeReal, profile.ModeReal
	c := NewConfig(fp)
	if c.Canvas || c.WebGL || !c.Audio {
		t.Fatalf("modes not mapped: %+v", c)
	}
}

// nodeHarness runs the injector in a Node vm with a minimal fake browser
// scope and prints the observed values as JSON.
const nodeHarness = `
const vm = require('vm'), fs = require('fs');
const src = fs.readFileSync(process.argv[2], 'utf8');
const ctx = vm.createContext({});
vm.runInContext(` + "`" + `
  class WorkerNavigator { get hardwareConcurrency(){ return 12 } get deviceMemory(){ return 16 }
    get platform(){ return 'MacIntel' } get languages(){ return ['en-US'] } get language(){ return 'en-US' } }
  globalThis.WorkerNavigator = WorkerNavigator;
  globalThis.navigator = new WorkerNavigator();
` + "`" + `, ctx);
vm.runInContext(src, ctx);
const out = vm.runInContext(` + "`" + `({
  hw: navigator.hardwareConcurrency, mem: navigator.deviceMemory, plat: navigator.platform,
  lang: navigator.language, langs: navigator.languages.join(','),
  getterSrc: Object.getOwnPropertyDescriptor(WorkerNavigator.prototype,'hardwareConcurrency').get.toString(),
  getterName: Object.getOwnPropertyDescriptor(WorkerNavigator.prototype,'hardwareConcurrency').get.name,
  tsSrc: Function.prototype.toString.toString(),
  tsOfFn: Function.prototype.toString.call(function f(){ return 1 }),
  ownKeys: Reflect.ownKeys(Object.getOwnPropertyDescriptor(WorkerNavigator.prototype,'platform').get).join(','),
})` + "`" + `, ctx);
// A second realm with its own patched toString must still see ctx's
// wrappers as native (CreepJS asks an iframe realm).
const ctx2 = vm.createContext({});
vm.runInContext(String.raw` + "`" + `class WorkerNavigator { get hardwareConcurrency(){ return 1 } }
  globalThis.WorkerNavigator = WorkerNavigator; globalThis.navigator = new WorkerNavigator();` + "`" + `, ctx2);
vm.runInContext(src, ctx2);
const getter = vm.runInContext("Object.getOwnPropertyDescriptor(WorkerNavigator.prototype,'hardwareConcurrency').get", ctx);
out.crossRealm = vm.runInContext('Function.prototype.toString', ctx2).call(getter);
out.crossRealmToString = vm.runInContext('Function.prototype.toString', ctx2).call(vm.runInContext('Function.prototype.toString', ctx));
console.log(JSON.stringify(out));
`

func TestInjectorInFakeWorkerScope(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	fp := profile.NewFingerprint(5, profile.Options{Platform: profile.PlatformWindows, Language: "fr-FR"})
	script, err := ScriptFor(fp)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	js := filepath.Join(dir, "inj.js")
	h := filepath.Join(dir, "h.js")
	if err := os.WriteFile(js, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h, []byte(nodeHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, h, js).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{
		`"plat":"Win32"`, `"lang":"fr-FR"`, `"langs":"fr-FR,fr"`,
		`"getterSrc":"function get hardwareConcurrency() { [native code] }"`,
		`"getterName":"get hardwareConcurrency"`,
		`"tsSrc":"function toString() { [native code] }"`,
		`"tsOfFn":"function f(){ return 1 }"`,
		`"ownKeys":"length,name"`,
		`"crossRealm":"function get hardwareConcurrency() { [native code] }"`,
		`"crossRealmToString":"function toString() { [native code] }"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if !strings.Contains(got, `"hw":`+itoa(fp.HardwareThreads)) || !strings.Contains(got, `"mem":`+itoa(fp.DeviceMemoryGB)) {
		t.Errorf("hardware not spoofed: %s", got)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
