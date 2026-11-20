package signature

// @module-feature confirm

import (
	"bytes"
	"encoding/base64"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// AllowedSignersFile is the trust list, relative to lattice/, in OpenSSH
// allowed_signers format with the code host login as the principal:
//
//	ana namespaces="lattice-decision" sk-ssh-ed25519@openssh.com AAAA... ana's key
const AllowedSignersFile = "allowed_signers"

// Signer is one usable line of allowed_signers.
type Signer struct {
	Principals []string
	Key        ssh.PublicKey
	// Namespaces is empty when the line does not restrict them.
	Namespaces []string
}

// ParseAllowedSigners reads allowed_signers. A line is used only when it is
// plain: exact principals (no wildcard patterns), a key, and at most a
// namespaces option. Lines with cert-authority, valid-after, valid-before or
// any other option, and lines that do not parse, are skipped and reported,
// so the list never grants more than it plainly says.
func ParseAllowedSigners(data []byte) (signers []Signer, skipped []string) {
	for i, line := range strings.Split(string(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		s, why := parseSignerLine(line)
		if why != "" {
			skipped = append(skipped, "line "+strconv.Itoa(i+1)+": "+why)
			continue
		}
		signers = append(signers, s)
	}
	return signers, skipped
}

func parseSignerLine(line string) (Signer, string) {
	tok := tokens(line)
	if len(tok) < 3 {
		return Signer{}, "needs principals, a key type and a key"
	}
	var s Signer
	for _, p := range strings.Split(unquote(tok[0]), ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.ContainsAny(p, "*?!") {
			return Signer{}, "principal patterns are not supported: " + p
		}
		s.Principals = append(s.Principals, p)
	}
	if len(s.Principals) == 0 {
		return Signer{}, "no principal"
	}
	rest := tok[1:]
	if _, ok := keyOf(rest); !ok {
		// The second field is an option list.
		for _, opt := range splitOptions(rest[0]) {
			name, value, _ := strings.Cut(opt, "=")
			switch strings.ToLower(name) {
			case "namespaces":
				for _, ns := range strings.Split(unquote(value), ",") {
					if ns = strings.TrimSpace(ns); ns != "" {
						s.Namespaces = append(s.Namespaces, ns)
					}
				}
			default:
				return Signer{}, "option " + name + " is not supported"
			}
		}
		rest = rest[1:]
	}
	key, ok := keyOf(rest)
	if !ok {
		return Signer{}, "needs a key type and a key"
	}
	s.Key = key
	return s, ""
}

// keyOf reads "keytype base64" from the start of fields. The key's own type
// must match the type field, so an option list is never taken for a key.
func keyOf(fields []string) (ssh.PublicKey, bool) {
	if len(fields) < 2 {
		return nil, false
	}
	raw, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return nil, false
	}
	key, err := ssh.ParsePublicKey(raw)
	if err != nil || key.Type() != fields[0] {
		return nil, false
	}
	return key, true
}

// Principals lists the logins allowed_signers grants key in namespace, sorted
// and without duplicates.
func Principals(signers []Signer, key ssh.PublicKey, namespace string) []string {
	if key == nil {
		return nil
	}
	want := key.Marshal()
	seen := map[string]bool{}
	var out []string
	for _, s := range signers {
		if !bytes.Equal(s.Key.Marshal(), want) {
			continue
		}
		if len(s.Namespaces) > 0 && !contains(s.Namespaces, namespace) {
			continue
		}
		for _, p := range s.Principals {
			if !seen[strings.ToLower(p)] {
				seen[strings.ToLower(p)] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

// tokens splits a line on whitespace, keeping double-quoted runs together.
func tokens(line string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range line {
		switch {
		case r == '"':
			quoted = !quoted
			cur.WriteRune(r)
		case (r == ' ' || r == '\t') && !quoted:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// splitOptions splits an option list on commas outside quotes.
func splitOptions(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			cur.WriteRune(r)
		case r == ',' && !quoted:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	return append(out, cur.String())
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
