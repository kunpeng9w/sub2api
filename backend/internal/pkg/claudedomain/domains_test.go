package claudedomain

import "testing"

func TestOfficialClaudeDomainBoundaries(t *testing.T) {
	for _, host := range []string{"anthropic.com", "api.anthropic.com", "CLAUDE.AI.", "platform.claude.com"} {
		if !ContainsHost(host) {
			t.Errorf("official host rejected: %s", host)
		}
	}
	for _, host := range []string{"anthropic.com.attacker.invalid", "notclaude.ai", "claude.com@attacker.invalid", "api.openai.com"} {
		if ContainsHost(host) {
			t.Errorf("unrelated host captured: %s", host)
		}
	}
}
