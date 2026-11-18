package core

// Triggers: flows that start by themselves. A flow's triggers are part of
// the flow (flow.Trigger, set up in the Flow Designer); what the trigger
// needs on this computer is kept here, in the app's folder:
//
//   - its state (next run, last run and how it went, how many runs,
//     missed runs), so schedules carry on when the app starts again;
//   - the secret values its runs use (the flow's secret inputs, its tools'
//     secrets), encrypted with AES-256-GCM. The key is kept in the system
//     keychain (or, where there is none, in a file only this user can read).
//
// The scheduler checks the open project's flows every few seconds. A run
// time that passed while the app was closed or the computer asleep runs
// once when it is back if the trigger says so (CatchUp), and is skipped
// otherwise. A trigger whose previous run is still going skips that time.
// Its runs are unattended: no review pauses, inputs from the trigger.
//
// A "flow" trigger starts when another flow's run ends (onFlowFinished); a
// chain of such runs carries the flows it went through, so a loop (A → B →
// A) or a chain longer than maxTriggerChain is refused. A "files" trigger
// collects the project's file changes that match it (noteFileChanges) and
// starts one run once they have been quiet for a few seconds; changes
// while the flow itself is running, and SpecWright's own .codegen folder,
// do not count, so a flow does not start itself again.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"codegen/internal/bus"
	"codegen/internal/flow"
	"codegen/internal/watch"
)

// RunTrigger says what started a run.
type RunTrigger struct {
	Kind string `json:"kind"` // schedule | flow | files
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Chain: the flows whose runs led to this one ("flow" triggers)
	Chain []string `json:"chain,omitempty"`
}

// maxTriggerChain is the most flows one chain of "flow" triggers runs.
const maxTriggerChain = 5

// TriggerState is what this computer knows about a trigger.
type TriggerState struct {
	Hash       string    `json:"hash"` // the settings the state is for (a change starts the count again)
	Since      time.Time `json:"since"`
	NextRun    time.Time `json:"nextRun,omitzero"`
	LastRun    time.Time `json:"lastRun,omitzero"`
	LastRunID  string    `json:"lastRunId,omitempty"`
	LastStatus string    `json:"lastStatus,omitempty"`
	LastError  string    `json:"lastError,omitempty"`
	Runs       int       `json:"runs"`
	Missed     int       `json:"missed"`
	Done       bool      `json:"done,omitempty"` // no more runs (once, Until, Count)
	// Secrets: name → encrypted value (base64 of nonce+ciphertext)
	Secrets map[string]string `json:"secrets,omitempty"`
}

// TriggerView is a trigger with its state, for the UI.
type TriggerView struct {
	FlowID   string       `json:"flowId"`
	FlowName string       `json:"flowName"`
	Trigger  flow.Trigger `json:"trigger"`
	State    TriggerState `json:"state"`
	Describe string       `json:"describe"`
	Upcoming []time.Time  `json:"upcoming"`
	// secrets: those its runs need, and those saved for it (names only)
	SecretsNeeded []string `json:"secretsNeeded"`
	SecretsSaved  []string `json:"secretsSaved"`
	KeyIn         string   `json:"keyIn"` // where the encryption key is: keychain | file
}

type triggerStore struct {
	mu   sync.Mutex
	path string
	data map[string]*TriggerState // "<flow>/<trigger>" → state
	// pend: file changes a "files" trigger collected, waiting to be quiet
	pend map[string]*filePend
}

type filePend struct {
	files map[string]bool
	timer *time.Timer
}

// triggerTick is how often the scheduler looks (replaceable in tests);
// triggerGrace is how late a run may start before it counts as missed.
var (
	triggerTick  = 15 * time.Second
	triggerGrace = 2 * time.Minute
	triggerNow   = time.Now
	// triggerQuiet is how long a files trigger waits for quiet (tests shorten it)
	triggerQuiet = func(w flow.FileWatch) time.Duration { return w.Quiet() }
)

func (h *Hub) triggers(root string) *triggerStore {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.trig != nil && h.trig.path != "" && strings.Contains(h.trig.path, workspaceKey(root)) {
		return h.trig
	}
	ts := &triggerStore{path: filepath.Join(h.Store.Dir(), "triggers", workspaceKey(root)+".json"), data: map[string]*TriggerState{}}
	if b, err := os.ReadFile(ts.path); err == nil {
		_ = json.Unmarshal(b, &ts.data)
	}
	h.trig = ts
	return ts
}

func workspaceKey(root string) string {
	h := sha256.Sum256([]byte(root))
	return hex.EncodeToString(h[:8])
}

func (ts *triggerStore) get(key string) *TriggerState {
	if s := ts.data[key]; s != nil {
		return s
	}
	s := &TriggerState{}
	ts.data[key] = s
	return s
}

// current is a trigger's state, started again when its settings changed.
func (ts *triggerStore) current(key string, t flow.Trigger, now time.Time) *TriggerState {
	s := ts.get(key)
	if s.Hash != t.Hash() {
		*s = TriggerState{Hash: t.Hash(), Since: now, Secrets: s.Secrets}
	}
	return s
}

func (ts *triggerStore) save() error {
	b, err := json.MarshalIndent(ts.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ts.path), 0o700); err != nil {
		return err
	}
	tmp := ts.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, ts.path)
}

// --- encryption --------------------------------------------------------------------

const triggerKeyAccount = "SpecWright trigger secrets key"

// triggerKey returns the key trigger secrets are encrypted with, making one
// the first time: in the system keychain, else in a file (0600).
func (h *Hub) triggerKey() ([]byte, string, error) {
	ref := flow.KeychainRef{Service: keychainService, Account: triggerKeyAccount}
	file := filepath.Join(h.Store.Dir(), "triggers", "key")
	decode := func(s string) []byte {
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		if err != nil || len(b) != 32 {
			return nil
		}
		return b
	}
	if KeychainAvailable() {
		if v, ok := keychainRead(ref); ok {
			if k := decode(v); k != nil {
				return k, "keychain", nil
			}
		}
	}
	if b, err := os.ReadFile(file); err == nil {
		if k := decode(string(b)); k != nil {
			return k, "file", nil
		}
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, "", err
	}
	enc := base64.StdEncoding.EncodeToString(k)
	if KeychainAvailable() {
		if err := keychainWrite(ref, enc); err == nil {
			return k, "keychain", nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(file, []byte(enc), 0o600); err != nil {
		return nil, "", err
	}
	return k, "file", nil
}

func seal(key []byte, plain string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

func unseal(key []byte, sealed string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(b) < gcm.NonceSize() {
		return "", errors.New("the saved secret is damaged")
	}
	out, err := gcm.Open(nil, b[:gcm.NonceSize()], b[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("the saved secret cannot be decrypted (the key changed); save it again")
	}
	return string(out), nil
}

// --- API ---------------------------------------------------------------------------

func trigKey(flowID, trigID string) string { return flowID + "/" + trigID }

// secretsNeeded: the flow's secret inputs and its tools' secrets.
func (h *Hub) secretsNeeded(f *flow.Flow) []string {
	seen := map[string]bool{}
	var out []string
	for _, in := range f.Inputs {
		if in.Type == "secret" && !seen[in.Name] {
			seen[in.Name] = true
			out = append(out, in.Name)
		}
	}
	if creds, err := h.FlowCredentials(f.ID); err == nil {
		for _, c := range creds {
			if !seen[c.Name] {
				seen[c.Name] = true
				out = append(out, c.Name)
			}
		}
	}
	return out
}

func (h *Hub) triggerView(root string, f *flow.Flow, t flow.Trigger, st TriggerState) TriggerView {
	v := TriggerView{FlowID: f.ID, FlowName: f.Name, Trigger: t, State: st, Upcoming: []time.Time{}, SecretsNeeded: h.secretsNeeded(f), SecretsSaved: []string{}}
	for k := range st.Secrets {
		v.SecretsSaved = append(v.SecretsSaved, k)
	}
	sort.Strings(v.SecretsSaved)
	v.State.Secrets = nil // names only leave the backend
	v.Describe = t.Describe(func(id string) string {
		if g, err := flow.Load(root, id); err == nil {
			return g.Name
		}
		return id
	})
	if t.Kind == "schedule" && t.Schedule != nil {
		if !st.Done {
			from := st.Since
			if from.IsZero() {
				from = triggerNow()
			}
			s := *t.Schedule
			if s.Count > 0 {
				s.Count = max(0, s.Count-st.Runs)
				if s.Count == 0 {
					s.Count = -1 // no more
				}
			}
			if s.Count >= 0 {
				v.Upcoming = s.Upcoming(triggerNow(), from, 3)
			}
		}
	}
	if KeychainAvailable() {
		v.KeyIn = "keychain"
	} else {
		v.KeyIn = "file"
	}
	return v
}

// FlowTriggers lists a flow's triggers with their state on this computer;
// flowID "" lists every flow's.
func (h *Hub) FlowTriggers(flowID string) ([]TriggerView, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	ts := h.triggers(root)
	var flows []*flow.Flow
	if flowID != "" {
		f, err := flow.Load(root, flowID)
		if err != nil {
			return nil, err
		}
		flows = []*flow.Flow{f}
	} else {
		list, _ := flow.List(root)
		for _, s := range list {
			if f, err := flow.Load(root, s.ID); err == nil && len(f.Triggers) > 0 {
				flows = append(flows, f)
			}
		}
	}
	out := []TriggerView{}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, f := range flows {
		for _, t := range f.Triggers {
			st := *ts.get(trigKey(f.ID, t.ID))
			if st.Hash != t.Hash() {
				st = TriggerState{Secrets: st.Secrets} // settings changed: shown as new
			}
			out = append(out, h.triggerView(root, f, t, st))
		}
	}
	return out, nil
}

// PreviewSchedule lists a schedule's next run times (the trigger form).
func (h *Hub) PreviewSchedule(s flow.Schedule) ([]time.Time, error) {
	if p := s.Check(); p != "" {
		return nil, errors.New(p)
	}
	now := triggerNow()
	return s.Upcoming(now, now, 5), nil
}

// SaveTriggerSecrets keeps secret values for a trigger's runs, encrypted on
// this computer; an empty value forgets one. It returns the names saved.
func (h *Hub) SaveTriggerSecrets(flowID, triggerID string, values map[string]string) ([]string, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	key, _, err := h.triggerKey()
	if err != nil {
		return nil, fmt.Errorf("could not get the encryption key: %v", err)
	}
	ts := h.triggers(root)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	st := ts.get(trigKey(flowID, triggerID))
	if st.Secrets == nil {
		st.Secrets = map[string]string{}
	}
	for k, v := range values {
		if v == "" {
			delete(st.Secrets, k)
			continue
		}
		s, err := seal(key, v)
		if err != nil {
			return nil, err
		}
		st.Secrets[k] = s
		h.Store.Redactor().Add(v)
	}
	out := []string{}
	for k := range st.Secrets {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, ts.save()
}

// RunTriggerNow starts a trigger's run now (its count goes up).
func (h *Hub) RunTriggerNow(flowID, triggerID string) (FlowRunState, error) {
	root, err := h.need()
	if err != nil {
		return FlowRunState{}, err
	}
	f, err := flow.Load(root, flowID)
	if err != nil {
		return FlowRunState{}, err
	}
	for _, t := range f.Triggers {
		if t.ID == triggerID {
			return h.fireTrigger(root, f, t, "run now", nil, nil)
		}
	}
	return FlowRunState{}, fmt.Errorf("the flow %s has no trigger %s", flowID, triggerID)
}

// fireTrigger starts a run of f for trigger t; vars are what started it
// ({{trigger.x}} in its input values), chain the flows that led to it.
func (h *Hub) fireTrigger(root string, f *flow.Flow, t flow.Trigger, why string, vars map[string]string, chain []string) (FlowRunState, error) {
	ts := h.triggers(root)
	key := trigKey(f.ID, t.ID)
	ts.mu.Lock()
	st := ts.get(key)
	sealed := map[string]string{}
	for k, v := range st.Secrets {
		sealed[k] = v
	}
	ts.mu.Unlock()
	o := FlowRunOptions{Inputs: map[string]string{}, Review: "none", Trigger: &RunTrigger{Kind: t.Kind, ID: t.ID, Name: firstNonEmpty(t.Name, t.ID), Chain: chain}}
	all := map[string]string{"name": firstNonEmpty(t.Name, t.ID), "kind": t.Kind}
	for k, v := range vars {
		all[k] = v
	}
	for k, v := range t.Inputs {
		o.Inputs[k] = flow.FillTriggerVars(v, all)
	}
	if len(sealed) > 0 {
		k, _, err := h.triggerKey()
		if err != nil {
			return FlowRunState{}, err
		}
		secretInput := map[string]bool{}
		for _, in := range f.Inputs {
			if in.Type == "secret" {
				secretInput[in.Name] = true
			}
		}
		for name, s := range sealed {
			v, err := unseal(k, s)
			if err != nil {
				return FlowRunState{}, fmt.Errorf("%s: %v", name, err)
			}
			if secretInput[name] {
				o.Inputs[name] = v // as if typed in the run form
			} else {
				h.SetSecret(name, v) // a tool's secret, for this session
			}
		}
	}
	st0, err := h.RunFlow(f.ID, o)
	ts.mu.Lock()
	s := ts.get(key)
	now := triggerNow()
	s.LastRun = now
	if err != nil {
		s.LastStatus, s.LastError = "failed", err.Error()
	} else {
		s.Runs++
		s.LastRunID, s.LastStatus, s.LastError = st0.ID, "running", ""
	}
	_ = ts.save()
	ts.mu.Unlock()
	h.Bus.Publish(bus.Event{Type: "trigger.fired", Summary: fmt.Sprintf("%s: %s (%s)", f.Name, firstNonEmpty(t.Name, t.ID), why),
		Data: map[string]any{"flow": f.ID, "trigger": t.ID, "run": st0.ID, "error": errText(err)}})
	return st0, err
}

// --- the scheduler ------------------------------------------------------------------

// runScheduler checks the project's triggers until ctx ends (the project
// closes or the app quits).
func (h *Hub) runScheduler(ctx context.Context, root string) {
	h.checkTriggers(root)
	t := time.NewTicker(triggerTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.checkTriggers(root)
		}
	}
}

// checkTriggers starts the runs that are due (one pass of the scheduler).
func (h *Hub) checkTriggers(root string) {
	list, err := flow.List(root)
	if err != nil {
		return
	}
	ts := h.triggers(root)
	now := triggerNow()
	type due struct {
		f   *flow.Flow
		t   flow.Trigger
		why string
	}
	var fire []due
	ts.mu.Lock()
	changed := false
	for _, sum := range list {
		f, err := flow.Load(root, sum.ID)
		if err != nil || len(f.Triggers) == 0 {
			continue
		}
		for _, t := range f.Triggers {
			if t.Kind != "schedule" { // flow and files triggers: only how their last run went
				s := ts.current(trigKey(f.ID, t.ID), t, now)
				if h.refreshLast(s) {
					changed = true
				}
				continue
			}
			if t.Schedule == nil || t.Schedule.Check() != "" {
				continue
			}
			s := ts.get(trigKey(f.ID, t.ID))
			if s.Hash != t.Hash() { // new, or its settings changed: start again
				*s = TriggerState{Hash: t.Hash(), Since: now, Secrets: s.Secrets}
				s.NextRun, _ = t.Schedule.Next(now, now)
				s.Done = s.NextRun.IsZero()
				changed = true
			}
			if !t.Enabled || s.Done {
				continue
			}
			if h.refreshLast(s) {
				changed = true
			}
			if s.NextRun.IsZero() || now.Before(s.NextRun) {
				continue
			}
			late := now.Sub(s.NextRun)
			next := func() {
				nx, ok := t.Schedule.Next(now, s.Since)
				s.NextRun = nx
				if !ok || (t.Schedule.Count > 0 && s.Runs >= t.Schedule.Count) {
					s.Done, s.NextRun = true, time.Time{}
				}
				changed = true
			}
			switch {
			case late > triggerGrace && !t.CatchUp:
				s.Missed++
				s.LastError = fmt.Sprintf("missed the run at %s (the app was closed or the computer asleep)", s.NextRun.Format("Jan 2 15:04"))
				next()
			case s.LastStatus == "running":
				s.LastError = fmt.Sprintf("skipped %s: the previous run was still going", s.NextRun.Format("Jan 2 15:04"))
				next()
			default:
				why := "scheduled"
				if late > triggerGrace {
					why = "catching up a run missed at " + s.NextRun.Format("Jan 2 15:04")
				}
				s.Runs++ // counted before it starts, so Count holds even if it fails to start
				next()
				s.Runs--
				fire = append(fire, due{f, t, why})
			}
		}
	}
	if changed {
		_ = ts.save()
	}
	ts.mu.Unlock()
	for _, d := range fire {
		_, _ = h.fireTrigger(root, d.f, d.t, d.why, nil, nil)
	}
}

var flowLive = []string{"running", "review", "scheduled"}

func slicesContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// refreshLast notes how a trigger's last run went once it has ended.
func (h *Hub) refreshLast(s *TriggerState) bool {
	if s.LastRunID == "" || s.LastStatus != "running" {
		return false
	}
	st, err := h.FlowRun(s.LastRunID)
	if err != nil || slicesContains(flowLive, st.Status) {
		return false
	}
	s.LastStatus, s.LastError = st.Status, st.Error
	return true
}

// flowRunning says whether a run of the flow is going (or waiting to start).
func (h *Hub) flowRunning(flowID string) bool {
	h.mu.RLock()
	var runs []*flowRun
	for _, fr := range h.flowRuns {
		runs = append(runs, fr)
	}
	h.mu.RUnlock()
	for _, fr := range runs {
		if st := fr.snapshot(); st.FlowID == flowID && st.Trial == nil && slicesContains(flowLive, st.Status) {
			return true
		}
	}
	return false
}

func (h *Hub) rootIs(root string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.root == root
}

// --- after another flow ------------------------------------------------------------

// onFlowFinished starts the flows whose "flow" triggers follow run.
func (h *Hub) onFlowFinished(root string, run FlowRunState) {
	if run.Trial != nil || !h.rootIs(root) {
		return
	}
	list, err := flow.List(root)
	if err != nil {
		return
	}
	var chain []string
	if run.Options.Trigger != nil {
		chain = append(chain, run.Options.Trigger.Chain...)
	}
	chain = append(chain, run.FlowID)
	output := ""
	for _, o := range run.Outputs {
		if strings.TrimSpace(o.Content) != "" {
			output = o.Content
			break
		}
	}
	vars := map[string]string{"flow": run.FlowID, "run": run.ID, "status": run.Status, "output": trunc(output, 20000), "error": run.Error}
	ts := h.triggers(root)
	for _, sum := range list {
		f, err := flow.Load(root, sum.ID)
		if err != nil {
			continue
		}
		for _, t := range f.Triggers {
			if t.Kind != "flow" || !t.Enabled || t.After == nil || t.After.Flow != run.FlowID || !t.After.Matches(run.Status) {
				continue
			}
			now := triggerNow()
			skip := ""
			switch {
			case slicesContains(chain, f.ID):
				skip = fmt.Sprintf("skipped at %s: it would loop (%s → %s)", now.Format("Jan 2 15:04"), strings.Join(chain, " → "), f.ID)
			case len(chain) >= maxTriggerChain:
				skip = fmt.Sprintf("skipped at %s: a chain runs at most %d flows (%s)", now.Format("Jan 2 15:04"), maxTriggerChain, strings.Join(chain, " → "))
			case h.flowRunning(f.ID):
				skip = fmt.Sprintf("skipped at %s: a run of this flow was still going", now.Format("Jan 2 15:04"))
			}
			if skip != "" {
				ts.mu.Lock()
				ts.current(trigKey(f.ID, t.ID), t, now).LastError = skip
				_ = ts.save()
				ts.mu.Unlock()
				continue
			}
			ts.mu.Lock()
			ts.current(trigKey(f.ID, t.ID), t, now)
			ts.mu.Unlock()
			_, _ = h.fireTrigger(root, f, t, fmt.Sprintf("after %s: %s", firstNonEmpty(run.FlowName, run.FlowID), run.Status), vars, chain)
		}
	}
}

// --- when files change -------------------------------------------------------------

// noteFileChanges collects the project's file changes (those no step run
// made) for the "files" triggers they match.
func (h *Hub) noteFileChanges(root string, chs []watch.Change) {
	if len(chs) == 0 || !h.rootIs(root) {
		return
	}
	list, err := flow.List(root)
	if err != nil {
		return
	}
	ts := h.triggers(root)
	for _, sum := range list {
		f, err := flow.Load(root, sum.ID)
		if err != nil {
			continue
		}
		for _, t := range f.Triggers {
			if t.Kind != "files" || !t.Enabled || t.Files == nil {
				continue
			}
			var hit []string
			for _, c := range chs {
				if t.Files.Matches(c.Path, c.Op) {
					hit = append(hit, c.Path)
				}
			}
			if len(hit) == 0 || h.flowRunning(f.ID) { // changes while it runs are likely its own
				continue
			}
			key, fid, tid, quiet := trigKey(f.ID, t.ID), f.ID, t.ID, triggerQuiet(*t.Files)
			ts.mu.Lock()
			if ts.pend == nil {
				ts.pend = map[string]*filePend{}
			}
			p := ts.pend[key]
			if p == nil {
				p = &filePend{files: map[string]bool{}}
				ts.pend[key] = p
				p.timer = time.AfterFunc(quiet, func() { h.fireFiles(root, fid, tid) })
			} else {
				p.timer.Reset(quiet)
			}
			for _, x := range hit {
				p.files[x] = true
			}
			ts.mu.Unlock()
		}
	}
}

// fireFiles starts a files trigger's run with the changes it collected.
func (h *Hub) fireFiles(root, flowID, trigID string) {
	if !h.rootIs(root) {
		return
	}
	ts := h.triggers(root)
	key := trigKey(flowID, trigID)
	f, err := flow.Load(root, flowID)
	var t *flow.Trigger
	if err == nil {
		for i := range f.Triggers {
			if f.Triggers[i].ID == trigID {
				t = &f.Triggers[i]
			}
		}
	}
	ts.mu.Lock()
	p := ts.pend[key]
	if p == nil {
		ts.mu.Unlock()
		return
	}
	if t == nil || !t.Enabled || t.Kind != "files" || t.Files == nil {
		delete(ts.pend, key) // removed or turned off meanwhile
		ts.mu.Unlock()
		return
	}
	if h.flowRunning(flowID) { // wait for the run to end, then try again
		p.timer.Reset(triggerQuiet(*t.Files))
		ts.mu.Unlock()
		return
	}
	delete(ts.pend, key)
	ts.current(key, *t, triggerNow())
	ts.mu.Unlock()
	var files []string
	for x := range p.files {
		files = append(files, x)
	}
	sort.Strings(files)
	shown := files
	if len(shown) > 200 {
		shown = append(append([]string{}, files[:200]...), fmt.Sprintf("… and %d more", len(files)-200))
	}
	vars := map[string]string{"files": strings.Join(shown, "\n"), "file": files[0]}
	_, _ = h.fireTrigger(root, f, *t, fmt.Sprintf("%d file(s) changed", len(files)), vars, nil)
}
