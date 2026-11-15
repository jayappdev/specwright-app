package core

// Flow runs: secret inputs — resolving them at start and asking for the
// ones that must be typed in when the run reaches them.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

// secretInput gets a secret input's value: what was typed in the run form
// (saved to the keychain when asked), else from where the input says.
func (h *Hub) secretInput(in flow.Input, typed string, o *FlowRunOptions) (string, error) {
	label, src, ref := firstNonEmpty(in.Label, in.Name), in.SecretSource(), in.KeychainFor()
	if typed != "" {
		if slices.Contains(o.SaveSecrets, in.Name) {
			if err := h.SaveToKeychain(ref, typed); err != nil {
				return "", fmt.Errorf("%s: %v", label, err)
			}
		}
		return typed, nil
	}
	fromKeychain := func() (string, bool) {
		if !KeychainAvailable() {
			return "", false
		}
		return kcGetRef(ref)
	}
	switch src {
	case "keychain":
		if v, ok := fromKeychain(); ok {
			return v, nil
		}
		return "", fmt.Errorf("%s: nothing found in %s. Type it in the run form (tick “Save to Keychain” to keep it), or add it with %s", label, describeRef(ref), keychainApp())
	case "env":
		if v, ok := lookupEnv(in.Env); ok && v != "" {
			return v, nil
		}
		return "", fmt.Errorf("%s: the environment variable %s is not set", label, in.Env)
	case "auto":
		if v, ok := fromKeychain(); ok {
			return v, nil
		}
		if in.Env != "" {
			if v, ok := lookupEnv(in.Env); ok && v != "" {
				return v, nil
			}
		}
		if in.Required {
			where := describeRef(ref)
			if in.Env != "" {
				where += " or $" + in.Env
			}
			return "", fmt.Errorf("%s: not found in %s; type it in the run form", label, where)
		}
	case "prompt":
		o.prompt = append(o.prompt, in)
	}
	return "", nil
}

// promptSecrets asks for the run's "prompt" secrets in a dialog (or on the
// terminal, headless), once the run has started; a value remembered for
// this session is used without asking.
func (h *Hub) promptSecrets(ctx context.Context, fr *flowRun) error {
	o := fr.st.Options
	if len(o.prompt) == 0 {
		return nil
	}
	key := func(in flow.Input) string { return "flow:" + fr.flow.ID + ":" + in.Name }
	var ask []extension.Secret
	vals := map[string]string{}
	h.sec.mu.RLock()
	for _, in := range o.prompt {
		if v := h.sec.values[key(in)]; v != "" {
			vals[in.Name] = v
			continue
		}
		ask = append(ask, extension.Secret{Name: in.Name, Desc: firstNonEmpty(strings.TrimSpace(in.Label+" "+in.Description), in.Name), Source: "ask"})
	}
	asker := h.sec.ask
	h.sec.mu.RUnlock()
	if len(ask) > 0 {
		if asker == nil {
			asker = h.askThroughUI
		}
		h.flowPublish(fr, "flow.secrets", fmt.Sprintf("Waiting for %d secret value(s)", len(ask)), nil)
		got, err := asker(withFlowStep(ctx, fr.st.ID, "start"), SecretRequest{Tool: fr.flow.Name, Secrets: ask, Reason: "The flow " + fr.flow.Name + " needs these to run."})
		if err != nil {
			return err
		}
		h.sec.mu.Lock()
		for _, s := range ask {
			if v := got[s.Name]; v != "" {
				vals[s.Name] = v
				if h.sec.values[s.Name] == v { // "remember for this session": kept for this flow's input, not as a tool secret
					h.sec.values[key(flow.Input{Name: s.Name})] = v
					delete(h.sec.values, s.Name)
				}
			}
		}
		h.sec.mu.Unlock()
	}
	for _, in := range o.prompt {
		if vals[in.Name] == "" && in.Required {
			return fmt.Errorf("%s was not provided", firstNonEmpty(in.Label, in.Name))
		}
	}
	fr.update(func(st *FlowRunState) {
		if st.Options.Secrets == nil {
			st.Options.Secrets = map[string]string{}
		}
		for k, v := range vals {
			h.Store.Redactor().Add(v)
			st.Options.Secrets[k] = v
			if v != "" {
				st.Options.Inputs[k] = flow.SecretMask
			}
		}
		st.Options.prompt = nil
	})
	return nil
}
