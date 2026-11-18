package main

import (
	"os"
	"reflect"
	"regexp"
	"testing"
)

// The desktop app calls only the methods frontend/src/backend.js lists
// (METHODS): a bound method missing from it is "not a function" in the app,
// while the browser preview (mock, or the dev server by reflection) works.
func TestFrontendListsEveryAppMethod(t *testing.T) {
	b, err := os.ReadFile("frontend/src/backend.js")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)const METHODS = \[(.*?)\];`).FindSubmatch(b)
	if m == nil {
		t.Fatal("METHODS not found in backend.js")
	}
	listed := map[string]bool{}
	for _, x := range regexp.MustCompile(`"(\w+)"`).FindAllSubmatch(m[1], -1) {
		listed[string(x[1])] = true
	}
	ty := reflect.TypeOf(&App{})
	for i := 0; i < ty.NumMethod(); i++ {
		name := ty.Method(i).Name
		if lifecycle[name] {
			continue
		}
		if !listed[name] {
			t.Errorf("App.%s is not in METHODS (frontend/src/backend.js): the desktop app cannot call it", name)
		}
	}
}

// lifecycle methods Wails calls itself, not the frontend
var lifecycle = map[string]bool{"Startup": true, "Shutdown": true, "DomReady": true, "BeforeClose": true}
