package audit

import "testing"

// A human confirmed value is never shown bare: it names how each approval
// reached human, once per method, and a value other than human is shown as is.
// @verifies confirm:INV-15
func TestConfirmedTextNamesTheMethod(t *testing.T) {
	row := func(value string, approvals ...Approval) Row {
		var r Row
		r.Signals.Confirmed = Signal{Value: value}
		r.Evidence.Approvals = approvals
		return r
	}
	signed := Approval{State: ConfirmedHuman, Via: ViaSignature, Method: "signed by ana, SHA256:abc"}
	reviewed := Approval{State: ConfirmedHuman, Via: ViaReview, Method: "approved by bo on PR #41"}

	cases := []struct {
		name string
		row  Row
		want string
	}{
		{"signature", row(ConfirmedHuman, signed), "human (signed by ana, SHA256:abc)"},
		{"review", row(ConfirmedHuman, reviewed), "human (approved by bo on PR #41)"},
		{"both, each once", row(ConfirmedHuman, signed, reviewed, signed), "human (signed by ana, SHA256:abc; approved by bo on PR #41)"},
		{"not human", row("agent", signed), "agent"},
		{"draft approval ignored", row(ConfirmedHuman, Approval{State: "draft", Method: "typed"}, reviewed), "human (approved by bo on PR #41)"},
	}
	for _, c := range cases {
		if got := ConfirmedText(c.row); got != c.want {
			t.Errorf("%s: ConfirmedText = %q, want %q", c.name, got, c.want)
		}
	}
}
