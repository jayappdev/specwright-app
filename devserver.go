package main

// Dev server: the real backend for the browser preview.
//
//	SpecWright devserver [-addr 127.0.0.1:5174] [-workspace DIR] [-store DIR]
//
// It runs core.Hub without the desktop window and serves the App methods the
// UI calls, so `npm run dev` (http://localhost:5173) can work against the real
// Go code instead of mockBackend.js. Open the printed URL; the page keeps the
// address and token for the session.
//
//	POST /api/call    {"method": "OpenWorkspace", "args": ["/path"]} -> {"result": …} or {"error": "…"}
//	GET  /api/events  server-sent events: every hub event, as "hub:event" in the desktop app
//
// It listens on the loopback interface only, needs the random token printed
// at start on every request (header X-Dev-Token, or ?token= for the event
// stream), and answers only the preview's origin. Methods that open a native
// dialog are refused: they need the desktop window.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
)

// methods that need the desktop window (a native file dialog)
var needsWindow = map[string]bool{"PickFolder": true, "PickDiagram": true, "SaveTextFile": true, "ExportProjectZip": true, "PickCAFile": true}

const previewOrigin = "http://localhost:5173"

func devServer(args []string) error {
	fs := flag.NewFlagSet("devserver", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:5174", "address to listen on (loopback only)")
	ws := fs.String("workspace", "", "project folder to open at start")
	storeDir := fs.String("store", "", "app data folder (default: the desktop app's own, so runs and settings are shared)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(*addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("the dev server only listens on the loopback interface, not %s", host)
	}
	app, err := newApp(*storeDir)
	if err != nil {
		return err
	}
	defer app.shutdown(context.Background())
	if *ws != "" {
		if _, err := app.hub.OpenWorkspace(*ws); err != nil {
			return fmt.Errorf("open %s: %w", *ws, err)
		}
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	token := hex.EncodeToString(b)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/call", func(w http.ResponseWriter, r *http.Request) {
		if !devAllowed(w, r, token) {
			return
		}
		var req struct {
			Method string            `json:"method"`
			Args   []json.RawMessage `json:"args"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			devReply(w, nil, err)
			return
		}
		res, err := devCall(app, req.Method, req.Args)
		devReply(w, res, err)
	})
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		if !devAllowed(w, r, token) {
			return
		}
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		ch, unsub := app.hub.Bus.Subscribe(1024, nil)
		defer unsub()
		fl.Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case e, ok := <-ch:
				if !ok {
					return
				}
				b, err := json.Marshal(e)
				if err != nil {
					continue
				}
				fmt.Fprintf(w, "data: %s\n\n", b)
				fl.Flush()
			}
		}
	})
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "SpecWright dev server on http://%s\nOpen the preview with the real backend:\n  %s/?devbackend=http://%s&token=%s\n", ln.Addr(), previewOrigin, ln.Addr(), token)
	return http.Serve(ln, mux)
}

// devAllowed checks the origin and the token, and answers CORS preflight.
func devAllowed(w http.ResponseWriter, r *http.Request, token string) bool {
	if o := r.Header.Get("Origin"); o != "" {
		if o != previewOrigin {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return false
		}
		w.Header().Set("Access-Control-Allow-Origin", o)
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Dev-Token")
		w.Header().Set("Vary", "Origin")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	got := r.Header.Get("X-Dev-Token")
	if got == "" {
		got = r.URL.Query().Get("token")
	}
	if got != token {
		http.Error(w, "wrong or missing token", http.StatusUnauthorized)
		return false
	}
	return true
}

// devCall calls an exported App method with JSON arguments.
func devCall(app *App, name string, args []json.RawMessage) (res any, err error) {
	if needsWindow[name] {
		return nil, fmt.Errorf("%s opens a native dialog and only works in the desktop app", name)
	}
	m := reflect.ValueOf(app).MethodByName(name)
	if !m.IsValid() || name == "" || strings.ToLower(name[:1]) == name[:1] {
		return nil, fmt.Errorf("no method %q", name)
	}
	t := m.Type()
	if len(args) > t.NumIn() {
		return nil, fmt.Errorf("%s takes %d arguments, got %d", name, t.NumIn(), len(args))
	}
	in := make([]reflect.Value, t.NumIn())
	for i := range in {
		p := reflect.New(t.In(i))
		if i < len(args) && string(args[i]) != "null" {
			if err := json.Unmarshal(args[i], p.Interface()); err != nil {
				return nil, fmt.Errorf("%s argument %d: %w", name, i+1, err)
			}
		}
		in[i] = p.Elem()
	}
	defer func() {
		if p := recover(); p != nil {
			log.Printf("devserver: %s panicked: %v", name, p)
			err = fmt.Errorf("%s failed: %v", name, p)
		}
	}()
	out := m.Call(in)
	errType := reflect.TypeOf((*error)(nil)).Elem()
	for _, v := range out {
		if v.Type().Implements(errType) {
			if !v.IsNil() {
				err = v.Interface().(error)
			}
		} else {
			res = v.Interface()
		}
	}
	return res, err
}

func devReply(w http.ResponseWriter, res any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if e := json.NewEncoder(w).Encode(map[string]any{"result": res}); e != nil {
		log.Printf("devserver: encode reply: %v", errors.Unwrap(e))
	}
}
