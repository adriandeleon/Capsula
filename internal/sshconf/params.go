package sshconf

import "strings"

// MergeParams applies a set of edited keyword values onto a block's existing
// parameters.
//
// This exists because an edit form can only show a handful of keywords, while a
// real Host block may carry dozens. A form that rebuilt the parameter list from
// its own fields would delete every keyword it does not know about — someone's
// ControlMaster, SetEnv, LocalForward — which is data loss disguised as an
// edit. So the original list is the base, and only managed keywords are
// touched.
//
// Rules:
//   - A managed keyword present in the block keeps its position; only its value
//     changes. Position matters: within a block ssh takes the first value it
//     finds for a keyword.
//   - A managed keyword whose new value is empty is removed.
//   - A managed keyword not already present is appended, in the order given by
//     managedOrder.
//   - Everything else is passed through untouched, including its comment.
//
// Only the FIRST occurrence of a managed keyword is edited. Keywords such as
// IdentityFile and LocalForward legitimately repeat, and a single-valued form
// field has nothing sensible to say about the second one, so later occurrences
// are preserved rather than clobbered.
func MergeParams(original []Param, edited map[string]string, managedOrder []string) []Param {
	managed := make(map[string]bool, len(managedOrder))
	for _, k := range managedOrder {
		managed[strings.ToLower(k)] = true
	}

	seen := map[string]bool{}
	out := make([]Param, 0, len(original)+len(managedOrder))

	for _, p := range original {
		lk := strings.ToLower(p.Key)
		if !managed[lk] || seen[lk] {
			// Not ours to touch, or a repeat occurrence we deliberately leave
			// alone.
			out = append(out, p)
			continue
		}
		seen[lk] = true
		val, ok := lookup(edited, lk)
		if !ok {
			out = append(out, p) // managed, but the form did not supply a value
			continue
		}
		if strings.TrimSpace(val) == "" {
			continue // cleared by the user: drop the line
		}
		out = append(out, Param{Key: p.Key, Value: val, Comment: p.Comment})
	}

	for _, k := range managedOrder {
		lk := strings.ToLower(k)
		if seen[lk] {
			continue
		}
		val, ok := lookup(edited, lk)
		if !ok || strings.TrimSpace(val) == "" {
			continue
		}
		out = append(out, Param{Key: k, Value: strings.TrimSpace(val)})
	}
	return out
}

func lookup(m map[string]string, lowerKey string) (string, bool) {
	for k, v := range m {
		if strings.ToLower(k) == lowerKey {
			return v, true
		}
	}
	return "", false
}

// ValidatePatterns reports whether a Host pattern list is well formed. Exported
// so a form can validate as the user types rather than only on submit.
func ValidatePatterns(patterns []string) error { return validatePatterns(patterns) }

// SplitPatterns parses the whitespace-separated pattern list from a text field.
func SplitPatterns(s string) []string { return strings.Fields(s) }

// Dirty reports whether any file in the set has unsaved changes.
func (s *Set) Dirty() bool {
	for _, f := range s.Files {
		if f.dirty {
			return true
		}
	}
	return false
}
