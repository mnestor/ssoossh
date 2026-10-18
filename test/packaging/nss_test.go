package packaging_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/nssd/config"
)

// nssExample is the example configuration the ssoosshd-nss package ships as
// /etc/ssoossh/nss.yaml. It lists every option at its default, commented
// out, and nssd/config holds the defaults themselves: the tests below keep
// the two from drifting apart.
const nssExample = "packaging/linux/nss.yaml"

// nssOptionLine is an option line in the example: "#name: value", with no
// space after the "#". Prose comments start "# ", so the two never mix.
var nssOptionLine = regexp.MustCompile(`^#([a-z_]+):(.*)$`)

// nssExampleOptions returns the example's option lines, uncommented, and the
// option names in the order they appear.
func nssExampleOptions(t *testing.T) (string, []string) {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), nssExample))
	if err != nil {
		t.Fatalf("read %s: %v", nssExample, err)
	}
	var lines, names []string
	for _, line := range strings.Split(string(body), "\n") {
		m := nssOptionLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		names = append(names, m[1])
		lines = append(lines, m[1]+":"+m[2])
	}
	return strings.Join(lines, "\n") + "\n", names
}

// nssConfigKeys returns every option nss.yaml accepts: the mapstructure tag
// of each config.Config field the file can set.
func nssConfigKeys() []string {
	var keys []string
	typ := reflect.TypeFor[config.Config]()
	for i := range typ.NumField() {
		tag := typ.Field(i).Tag.Get("mapstructure")
		if tag != "" && tag != "-" {
			keys = append(keys, tag)
		}
	}
	return keys
}

// should list every nss.yaml option in the example exactly once, so an
// option added to nssd/config cannot ship undocumented.
func TestNSSExampleShouldListEveryOptionOnce(t *testing.T) {
	t.Parallel()

	_, names := nssExampleOptions(t)
	for _, key := range nssConfigKeys() {
		if n := countOf(names, key); n != 1 {
			t.Errorf("%s lists %q %d times, want once", nssExample, key, n)
		}
	}
	for _, name := range names {
		if !slices.Contains(nssConfigKeys(), name) {
			t.Errorf("%s lists %q, which nss.yaml does not accept", nssExample, name)
		}
	}
}

// countOf counts the occurrences of s in list.
func countOf(list []string, s string) int {
	n := 0
	for _, v := range list {
		if v == s {
			n++
		}
	}
	return n
}

// should give every option the value the daemon uses when the option is
// absent, so the example never documents a default the binary does not
// have. The example is loaded by the daemon's own loader, uncommented,
// with only the two options that have no default (the server and a TLS
// trust root) filled in.
func TestNSSExampleShouldMatchTheDaemonDefaults(t *testing.T) {
	t.Parallel()

	options, _ := nssExampleOptions(t)
	const server, pin = "https://sso.example.test", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	options = strings.Replace(options, "server: \"\"\n", "server: \""+server+"\"\n", 1)
	options = strings.Replace(options, "tls_spki_pin: []\n", "tls_spki_pin: [\""+pin+"\"]\n", 1)

	path := filepath.Join(t.TempDir(), "nss.yaml")
	if err := os.WriteFile(path, []byte(options), 0o600); err != nil {
		t.Fatalf("write the uncommented example: %v", err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("the uncommented example does not load: %v", err)
	}

	want := config.Defaults()
	want.Server, want.TLSSPKIPin, want.Domain = server, []string{pin}, got.Domain
	for _, diff := range configDiff(*got, want) {
		t.Errorf("%s: %s", nssExample, diff)
	}
}

// configDiff lists, by option name, every field where got and want differ.
// An empty slice or map equals a nil one: "allowed_shells: []" in the
// example is the same setting as the default's nil.
func configDiff(got, want config.Config) []string {
	var diffs []string
	gv, wv := reflect.ValueOf(got), reflect.ValueOf(want)
	typ := gv.Type()
	for i := range typ.NumField() {
		g, w := gv.Field(i), wv.Field(i)
		if isEmptyCollection(g) && isEmptyCollection(w) {
			continue
		}
		if !reflect.DeepEqual(g.Interface(), w.Interface()) {
			name := typ.Field(i).Tag.Get("mapstructure")
			diffs = append(diffs, fmt.Sprintf("%s is %v in the example, the daemon's default is %v", name, g.Interface(), w.Interface()))
		}
	}
	return diffs
}

// isEmptyCollection reports whether v is a nil or empty slice or map.
func isEmptyCollection(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	default:
		return false
	}
}

// should leave every option commented out, so installing the package
// changes nothing until an administrator decides what the host trusts.
func TestNSSExampleShouldSetNothing(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), nssExample))
	if err != nil {
		t.Fatalf("read %s: %v", nssExample, err)
	}
	for i, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			t.Errorf("%s line %d is an active setting: %q", nssExample, i+1, trimmed)
		}
	}
}
