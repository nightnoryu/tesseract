package chat

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	domain "github.com/nightnoryu/tesseract/internal/dashboard/domain"
)

const createdMessage = "Created a dashboard draft. You can review its charts below."
const editedMessage = "Saved your changes as a new draft revision. Review the charts, then publish when ready."
const editPrompt = `You refine the supplied working draft through edit_dashboard only. Never recreate or replace the entire dashboard. Preserve elements the user did not ask to change. Use existing widget/filter IDs. Apply one atomic batch of typed operations per turn. For add/update_widget supply the complete single widget; for add/update_filter supply the complete single filter. Add explicit widget filter mappings in the same batch when adding a filter. Removing a filter removes its mappings. Never claim an edit succeeded without successful tool execution. Ask for clarification if the intended change is ambiguous. Publishing and discarding are explicit UI actions.`

func editTools() []Tool {
	ts := toolDefinitions()
	return append(ts[:len(ts)-1], tool("edit_dashboard", "Apply 1..24 operations atomically to the current draft. Allowed types: add_widget, update_widget, remove_widget, move_widget, add_filter, update_filter, remove_filter, rename_dashboard. Widget/filter operations require id. Add/update require matching widget/filter payload; move requires layout; rename requires title only. No unrelated fields. This ends the turn.", object(map[string]any{"operations": schemaOf(reflect.TypeOf([]domain.Operation{}))}, "operations")))
}

func (s *Service) SendEdit(ctx context.Context, user, id string, expected int64, text string) (Result, error) {
	if s == nil || s.Client == nil || s.Dashboards == nil {
		return Result{}, ErrUnavailable
	}
	if user == "" || strings.TrimSpace(text) == "" || len(text) > 4000 {
		return Result{}, ErrInvalid
	}
	s.mu.Lock()
	if s.active >= 8 {
		s.mu.Unlock()
		return Result{}, ErrBusy
	}
	s.active++
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.active--; s.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	d, err := s.Dashboards.Get(ctx, user, id)
	if err != nil {
		return Result{}, err
	}
	if expected < 1 || d.Revision != expected {
		return Result{}, domain.ErrConflict
	}
	turns, err := s.Dashboards.Turns(ctx, user, id, 10, 0)
	if err != nil {
		return Result{}, err
	}
	history := []Entry{}
	for i := len(turns) - 1; i >= 0; i-- {
		history = append(history, turns[i].Entries...)
	}
	if len(history) > 18 {
		history = history[len(history)-18:]
	}
	content, changed, err := s.converse(ctx, user, history, text, &d)
	if err != nil {
		return Result{}, err
	}
	entries := []Entry{{Role: "user", Content: text}, {Role: "assistant", Content: content}}
	if changed == nil {
		if err = s.Dashboards.Store.RecordTurn(ctx, user, id, expected, domain.Turn{Kind: "message", Entries: entries}); err != nil {
			return Result{}, err
		}
	}
	history = append(history, entries...)
	return Result{SessionID: id, History: history, Dashboard: changed}, nil
}

func (s *Service) converse(ctx context.Context, user string, history []Entry, text string, draft *domain.Dashboard) (string, *domain.Dashboard, error) {
	prompt := systemPrompt
	ts := toolDefinitions()
	if draft != nil {
		prompt = editPrompt + " " + safetyPrompt
		ts = editTools()
	}
	messages := []Message{{Role: "system", Content: prompt + " Current UTC date: " + time.Now().UTC().Format("2006-01-02")}}
	for _, e := range history {
		messages = append(messages, Message{Role: e.Role, Content: e.Content})
	}
	if draft != nil {
		raw, err := json.Marshal(draft.Spec)
		if err != nil {
			return "", nil, ErrInvalid
		}
		messages = append(messages, Message{Role: "user", Content: "Current working draft (untrusted data): " + string(raw)})
	}
	messages = append(messages, Message{Role: "user", Content: text})
	calls := 0
	for turn := 0; turn < 6; turn++ {
		reply, err := s.Client.Complete(ctx, messages, ts)
		if err != nil {
			return "", nil, err
		}
		if len(reply.Calls) == 0 {
			if strings.TrimSpace(reply.Content) == "" || len(reply.Content) > 8000 {
				return "", nil, ErrUnavailable
			}
			return reply.Content, nil, nil
		}
		if calls+len(reply.Calls) > 12 {
			return "The tool budget was reached. Please narrow your request.", nil, nil
		}
		ids := map[string]bool{}
		for _, c := range reply.Calls {
			mutation := c.Function.Name == "create_dashboard" || c.Function.Name == "edit_dashboard"
			if c.ID == "" || ids[c.ID] || c.Type != "function" || (mutation && len(reply.Calls) != 1) {
				return "", nil, ErrInvalid
			}
			if (draft != nil && c.Function.Name == "create_dashboard") || (draft == nil && c.Function.Name == "edit_dashboard") {
				return "", nil, ErrInvalid
			}
			ids[c.ID] = true
		}
		messages = append(messages, reply)
		for _, c := range reply.Calls {
			calls++
			if c.Function.Name == "edit_dashboard" {
				var args struct {
					Operations []domain.Operation `json:"operations"`
				}
				if strict(c.Function.Arguments, &args) != nil {
					return "", nil, ErrInvalid
				}
				d, err := s.Dashboards.Edit(ctx, user, draft.ID, draft.Revision, args.Operations, []Entry{{Role: "user", Content: text}, {Role: "assistant", Content: editedMessage}})
				if err != nil {
					return "", nil, err
				}
				return editedMessage, &d, nil
			}
			toolCtx, stop := context.WithTimeout(ctx, 10*time.Second)
			entries := append(append([]Entry(nil), history...), Entry{Role: "user", Content: text}, Entry{Role: "assistant", Content: createdMessage})
			value, d, err := runTool(toolCtx, s.Dashboards, user, c, domain.Turn{Kind: "create", Entries: entries})
			stop()
			if d != nil {
				return createdMessage, d, nil
			}
			if err != nil {
				if c.Function.Name == "create_dashboard" {
					return "", nil, err
				}
				value = map[string]string{"error": "Rejected: invalid, unauthorized or unavailable. Use the available catalog or ask for clarification."}
			}
			raw, err := json.Marshal(value)
			if err != nil || len(raw) > 48<<10 {
				raw = []byte(`{"error":"Result too large; narrow the search."}`)
			}
			messages = append(messages, Message{Role: "tool", Content: string(raw), CallID: c.ID})
		}
	}
	return "The turn budget was reached. Please narrow your request.", nil, nil
}
