package postgres

// @module-feature adapters.postgres

import (
	"regexp"
	"strings"
)

// A function that reads a URL whole from Supabase Vault has no literal path
// for an origin rule to join. A person declares which route each secret holds
// (workspace.yaml http.secrets: <secret name>: <route path>), and a request
// whose URL is exactly the variable that read the secret, or that variable
// followed by literals, takes its key from the declared path.

// vaultRead is one `select decrypted_secret ... where name = '<secret>'` that
// assigns a variable, and where in the statement it is.
type vaultRead struct {
	secret string
	at     int
}

var (
	// vaultFrom is the read itself, up to its WHERE clause: select
	// decrypted_secret from vault.decrypted_secrets.
	vaultFrom = `select\s+(?:[a-z_][a-z0-9_]*\.)?decrypted_secret\s+`
	vaultView = `from\s+vault\s*\.\s*decrypted_secrets\b`

	// reVaultInto: select decrypted_secret into <var> from vault.decrypted_secrets where ...
	reVaultInto = regexp.MustCompile(`(?is)\b` + `select\s+(?:[a-z_][a-z0-9_]*\.)?decrypted_secret\s+into\s+(?:strict\s+)?([a-z_][a-z0-9_]*)\s+` + vaultView + `([^;]*)`)
	// reVaultAssign: <var> := (select decrypted_secret from vault.decrypted_secrets where ...).
	reVaultAssign = regexp.MustCompile(`(?is)\b([a-z_][a-z0-9_]*)\s*:=\s*\(\s*` + vaultFrom + vaultView + `([^;)]*)\)`)
	// reVaultDeclare is the same in a declaration: <var> <type> := (select ...).
	reVaultDeclare = regexp.MustCompile(`(?is)\b([a-z_][a-z0-9_]*)\s+[a-z_][a-z0-9_]*\s*:=\s*\(\s*` + vaultFrom + vaultView + `([^;)]*)\)`)
	// reVaultSub is the same read as a scalar subquery standing for the URL.
	reVaultSub = regexp.MustCompile(`(?is)^\(\s*` + vaultFrom + vaultView + `([^;)]*)\)\s*(?:::\s*[a-z_]+)?$`)
	// reVaultName reads name = '<literal>' out of a WHERE clause, a table
	// alias allowed.
	reVaultName = regexp.MustCompile(`(?is)\bname\s*=\s*'([^']*)'`)
	reOr        = regexp.MustCompile(`(?is)\bor\b`)
	reVarOnly   = regexp.MustCompile(`(?is)^\(*\s*([a-z_][a-z0-9_]*)\s*(?:::\s*[a-z_]+)?\s*\)*$`)
)

// SetHTTPSecrets declares the secrets of workspace.yaml http.secrets: a
// Vault secret name mapped to the route path the secret's URL points at. An
// entry with an empty name or a path that does not start with "/" is ignored.
// Call it before parsing.
func (a *Adapter) SetHTTPSecrets(secrets map[string]string) {
	a.opts.secrets = nil
	for name, p := range secrets {
		if name == "" || !strings.HasPrefix(p, "/") {
			continue
		}
		if a.opts.secrets == nil {
			a.opts.secrets = map[string]string{}
		}
		a.opts.secrets[name] = p
	}
}

// secretName reads the secret a WHERE clause names with name = '<literal>'.
// ok is false when it names none, several, or is an alternative (or): the
// read is then not one secret.
func secretName(clause string) (string, bool) {
	if reOr.MatchString(clause) {
		return "", false
	}
	m := reVaultName.FindAllStringSubmatch(clause, -1)
	if len(m) != 1 {
		return "", false
	}
	return m[0][1], true
}

// plpgsqlKeywords are words that can stand before a variable in a statement,
// so a declaration pattern must not take one for the variable's name.
var plpgsqlKeywords = map[string]bool{"begin": true, "then": true, "else": true, "loop": true, "declare": true}

// vaultReads finds the variables a statement sets from one Vault secret.
// Only a straight-line, single assignment counts: a variable assigned any
// other way as well (:=, =, into, a for loop), or read from a secret whose
// name is not one literal, is left out, so a URL built on it stays computed.
// @enforces reachability:INV-11
func vaultReads(text string) map[string]vaultRead {
	code := maskSQL(text, true)
	found := map[string]vaultRead{}
	add := func(re *regexp.Regexp) {
		for _, m := range re.FindAllStringSubmatchIndex(code, -1) {
			name, ok := secretName(code[m[4]:m[5]])
			v := strings.ToLower(code[m[2]:m[3]])
			if !ok || plpgsqlKeywords[v] {
				continue
			}
			if _, dup := found[v]; dup {
				found[v] = vaultRead{at: -1} // read twice: never a single assignment
				continue
			}
			found[v] = vaultRead{secret: name, at: m[0]}
		}
	}
	add(reVaultInto)
	add(reVaultAssign)
	add(reVaultDeclare)
	for v, r := range found {
		if r.at < 0 || assignments(code, v) != 1 {
			delete(found, v)
		}
	}
	return found
}

// assignments counts the places a PL/pgSQL variable is assigned: v := ...,
// v = ... at the start of a statement, select ... into v, and for v in.
func assignments(code, v string) int {
	q := regexp.QuoteMeta(v)
	n := 0
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`(?is)\b` + q + `\b(?:\s+[a-z_][a-z0-9_]*)?\s*:=`),
		regexp.MustCompile(`(?is)(?:;|\bbegin\b|\bthen\b|\belse\b|\bloop\b)\s*` + q + `\s*=[^=]`),
		regexp.MustCompile(`(?is)\binto\s+(?:strict\s+)?(?:[a-z_][a-z0-9_]*\s*,\s*)*` + q + `\b`),
		regexp.MustCompile(`(?is)\bfor\s+` + q + `\s+in\b`),
	} {
		n += len(re.FindAllStringIndex(code, -1))
	}
	return n
}

// vaultURL reads a URL expression that is a Vault secret read whole, alone or
// followed by string literals. secret is the secret's name; path is the
// declared route path plus the literals, empty when the secret is not
// declared. ok is false for any other expression.
// @enforces reachability:INV-11
func (o refOpts) vaultURL(expr string, at int, reads map[string]vaultRead) (path, secret string, ok bool) {
	parts := splitConcat(expr)
	if len(parts) == 0 {
		return "", "", false
	}
	first := strings.TrimSpace(parts[0])
	if m := reVaultSub.FindStringSubmatch(first); m != nil {
		secret, ok = secretName(m[1])
	} else if m := reVarOnly.FindStringSubmatch(first); m != nil {
		var r vaultRead
		if r, ok = reads[strings.ToLower(m[1])]; ok && r.at < at {
			secret = r.secret
		} else {
			ok = false
		}
	}
	if !ok {
		return "", "", false
	}
	var suffix strings.Builder
	for _, p := range parts[1:] {
		lit, isLit := stringLiteral(p)
		if !isLit {
			return "", "", false
		}
		suffix.WriteString(lit)
	}
	if base, declared := o.secrets[secret]; declared {
		return base + suffix.String(), secret, true
	}
	return "", secret, true
}
