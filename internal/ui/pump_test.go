package ui

import (
	"reflect"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// pump is a miniature Bubble Tea runtime for tests.
//
// It exists because parts of the interface are message-driven rather than
// synchronous: huh advances between form fields by returning commands whose
// resulting messages perform the move. Calling Update alone therefore leaves a
// form stuck on its first field — typed text lands in the wrong place and the
// form never completes — which looks exactly like an application bug but is
// only a test harness that is not a runtime.
//
// Commands that do not return promptly are dropped. Those are timers, chiefly
// the cursor blink, and waiting on them would make the tests take seconds each
// while proving nothing.
func pump(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; steps < 500 && len(queue) > 0; steps++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}

		ch := make(chan tea.Msg, 1)
		go func() { ch <- c() }()
		var msg tea.Msg
		select {
		case msg = <-ch:
		case <-time.After(30 * time.Millisecond):
			continue // a timer; nothing here depends on it
		}
		if msg == nil {
			continue
		}

		// tea.Batch yields an exported BatchMsg but tea.Sequence yields an
		// unexported type, so both are recognised structurally as a slice of
		// commands rather than by type assertion.
		if rv := reflect.ValueOf(msg); rv.Kind() == reflect.Slice &&
			rv.Type().Elem() == reflect.TypeOf(tea.Cmd(nil)) {
			for i := range rv.Len() {
				queue = append(queue, rv.Index(i).Interface().(tea.Cmd))
			}
			continue
		}

		u, next := m.Update(msg)
		m = u.(Model)
		queue = append(queue, next)
	}
	return m
}

// sendKey delivers a key and then runs the commands it produced, as the real
// runtime would.
func sendKey(t *testing.T, m Model, msg tea.KeyMsg) Model {
	t.Helper()
	u, cmd := m.Update(msg)
	return pump(t, u.(Model), cmd)
}

func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		m = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func enter(t *testing.T, m Model) Model {
	t.Helper()
	return sendKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
}
