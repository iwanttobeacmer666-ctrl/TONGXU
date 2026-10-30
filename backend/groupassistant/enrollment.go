package groupassistant

import "strings"

// Missing enrollment fails closed for non-owners. Store membership records are
// historical file ownership, never evidence of current enrollment.
func ValidateMembers(owner string, groups []string, members map[string][]string) error {
	active := make(map[string]bool, len(groups))
	for _, group := range groups {
		active[group] = true
	}
	for group, users := range members {
		if !active[group] {
			return ErrInvalid
		}
		seen := map[string]bool{}
		for _, user := range users {
			if user != strings.TrimSpace(user) || user == "" || user == owner || len(user) > 250 ||
				strings.ContainsAny(user, "\x00\r\n") || strings.HasPrefix(user, "wecom:") || strings.HasPrefix(user, "YOUR_") || seen[user] {
				return ErrInvalid
			}
			seen[user] = true
		}
	}
	return nil
}
