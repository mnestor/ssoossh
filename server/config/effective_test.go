package config

import (
	"bytes"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
)

// Test methodology: the walker is exercised against small synthetic structs
// declared here rather than against Config, so each Go kind and tag form has
// one case that says what it is for. Two tests then hold the walker against
// the real thing: every key defaults.yaml ships must appear in the view, and
// every leaf whose name says it holds a credential must be redacted. Those
// two are the drift guards, and the reason this view reflects over the
// struct rather than listing keys by hand.

// Scalars covers each basic kind the configuration uses.
type Scalars struct {
	Text  string  `mapstructure:"text"`
	Flag  bool    `mapstructure:"flag"`
	Count int     `mapstructure:"count"`
	Size  uint16  `mapstructure:"size"`
	Rate  float64 `mapstructure:"rate"`
}

// Pointered holds the optional-flag shape Config uses for fips and
// cookie_secure, where nil and false mean different things.
type Pointered struct {
	Flag *bool `mapstructure:"flag"`
}

// Waiting holds a duration, whose String is what the view should show.
type Waiting struct {
	Wait time.Duration `mapstructure:"wait"`
}

// Schedule renders itself, and does so from a pointer receiver — the same
// declaration PolicyCondition uses, reachable only through an addressable
// value.
type Schedule struct {
	Seconds int `mapstructure:"seconds"`
}

func (s *Schedule) String() string { return "every " + strconv.Itoa(s.Seconds) + "s" }

// Scheduled holds a Schedule by value, so the walk has to take its address
// to find the String method.
type Scheduled struct {
	Schedule Schedule `mapstructure:"schedule"`
}

// Listed holds a list of scalars, which belong on one line.
type Listed struct {
	Names []string `mapstructure:"names"`
}

// Tier is one element of a list of structs, which has to be indexed instead.
type Tier struct {
	Name string `mapstructure:"name"`
}

// Tiered holds that list.
type Tiered struct {
	Tiers []Tier `mapstructure:"tiers"`
}

// Mapped holds the claim-name map shape from authentication.fields.extra.
type Mapped struct {
	Extra map[string]string `mapstructure:"extra"`
}

// Nested holds a plain sub-struct, which takes a key of its own.
type Nested struct {
	TLS TLSLike `mapstructure:"tls"`
}

// TLSLike stands in for the tls block.
type TLSLike struct {
	MinVersion string `mapstructure:"min_version"`
}

// Inner is the struct both squash forms lift into their parent.
type Inner struct {
	Key string `mapstructure:"key"`
}

// SquashTagged squashes by tag, the form SignerConfig takes in Config.
type SquashTagged struct {
	Inner Inner `mapstructure:",squash"`
}

// SquashEmbedded squashes by embedding without a tag, the form the
// timberjack logger and CertificateInfo arrive in.
type SquashEmbedded struct {
	Inner
}

// Skipping holds a field tagged out of the configuration entirely.
type Skipping struct {
	Kept    string `mapstructure:"kept"`
	Ignored string `mapstructure:"-"`
}

// Hiding holds an unexported field, which is not configuration and which
// reflection cannot read anyway.
type Hiding struct {
	Kept   string `mapstructure:"kept"`
	hidden string // declared so the walk has one to skip
}

// Untagged holds a field with no mapstructure tag, the form the embedded
// logger's own fields take.
type Untagged struct {
	MaxSize int
}

func TestEffectiveWalk(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input any
		want  []Setting
	}{
		{
			name:  "should render each scalar kind as text",
			input: &Scalars{Text: "hello", Flag: true, Count: -3, Size: 9, Rate: 10},
			want: []Setting{
				{Key: "text", Value: "hello"},
				{Key: "flag", Value: "true"},
				{Key: "count", Value: "-3"},
				{Key: "size", Value: "9"},
				{Key: "rate", Value: "10"},
			},
		},
		{
			name:  "should render an unset string as empty",
			input: &Scalars{},
			want: []Setting{
				{Key: "text", Value: ""},
				{Key: "flag", Value: "false"},
				{Key: "count", Value: "0"},
				{Key: "size", Value: "0"},
				{Key: "rate", Value: "0"},
			},
		},
		{
			name:  "should render a nil pointer as unset",
			input: &Pointered{},
			want:  []Setting{{Key: "flag", Value: ""}},
		},
		{
			name:  "should render what a set pointer points at",
			input: &Pointered{Flag: boolPtr(false)},
			want:  []Setting{{Key: "flag", Value: "false"}},
		},
		{
			name:  "should render a duration in its own text form",
			input: &Waiting{Wait: 90 * time.Second},
			want:  []Setting{{Key: "wait", Value: "1m30s"}},
		},
		{
			name:  "should reach a String method declared on the pointer receiver",
			input: &Scheduled{Schedule: Schedule{Seconds: 5}},
			want:  []Setting{{Key: "schedule", Value: "every 5s"}},
		},
		{
			name:  "should join a list of scalars onto one line",
			input: &Listed{Names: []string{"permit-pty", "permit-agent-forwarding"}},
			want:  []Setting{{Key: "names", Value: "permit-pty, permit-agent-forwarding"}},
		},
		{
			name:  "should render an empty list as unset",
			input: &Listed{Names: []string{}},
			want:  []Setting{{Key: "names", Value: ""}},
		},
		{
			name:  "should index into a list of structs",
			input: &Tiered{Tiers: []Tier{{Name: "high"}, {Name: "low"}}},
			want: []Setting{
				{Key: "tiers[0].name", Value: "high"},
				{Key: "tiers[1].name", Value: "low"},
			},
		},
		{
			name:  "should render an empty map as unset",
			input: &Mapped{},
			want:  []Setting{{Key: "extra", Value: ""}},
		},
		{
			name:  "should order map keys so the view is stable",
			input: &Mapped{Extra: map[string]string{"zone": "z", "dept": "d"}},
			want: []Setting{
				{Key: "extra.dept", Value: "d"},
				{Key: "extra.zone", Value: "z"},
			},
		},
		{
			name:  "should nest a struct under its own key",
			input: &Nested{TLS: TLSLike{MinVersion: "TLS1.3"}},
			want:  []Setting{{Key: "tls.min_version", Value: "TLS1.3"}},
		},
		{
			name:  "should lift a squash-tagged struct into its parent",
			input: &SquashTagged{Inner: Inner{Key: "value"}},
			want:  []Setting{{Key: "key", Value: "value"}},
		},
		{
			name:  "should lift an untagged embedded struct into its parent",
			input: &SquashEmbedded{Inner: Inner{Key: "value"}},
			want:  []Setting{{Key: "key", Value: "value"}},
		},
		{
			name:  "should skip a field tagged out of the configuration",
			input: &Skipping{Kept: "yes", Ignored: "no"},
			want:  []Setting{{Key: "kept", Value: "yes"}},
		},
		{
			name:  "should skip an unexported field",
			input: &Hiding{Kept: "yes", hidden: "no"},
			want:  []Setting{{Key: "kept", Value: "yes"}},
		},
		{
			name:  "should fall back to the lowercased name of an untagged field",
			input: &Untagged{MaxSize: 100},
			want:  []Setting{{Key: "maxsize", Value: "100"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := walkValue(tc.input)

			assertSettings(t, got, tc.want)
		})
	}
}

// SecretHolder covers the three ways a secret reaches the view: configured,
// unset, and inherited from a tagged parent.
type SecretHolder struct {
	Password string      `mapstructure:"password" secret:"true"`
	Unset    string      `mapstructure:"unset" secret:"true"`
	Block    SecretBlock `mapstructure:"block" secret:"true"`
}

// SecretBlock is the tagged parent whose fields inherit the tag.
type SecretBlock struct {
	Inner string `mapstructure:"inner"`
}

func TestEffectiveWalk_ShouldRedactSecretsWithoutHidingWhetherOneIsSet(t *testing.T) {
	t.Parallel()

	held := &SecretHolder{Password: "hunter2", Block: SecretBlock{Inner: "also secret"}}

	got := walkValue(held)

	assertSettings(t, got, []Setting{
		{Key: "password", Value: Redacted, Secret: true},
		{Key: "unset", Value: "", Secret: true},
		{Key: "block.inner", Value: Redacted, Secret: true},
	})
}

// Cyclic points at itself, which the real configuration never does. It is
// here so the depth cap has something to stop.
type Cyclic struct {
	Next *Cyclic `mapstructure:"next"`
	Leaf string  `mapstructure:"leaf"`
}

func TestEffectiveWalk_ShouldStopOnACycleRatherThanRecurseForever(t *testing.T) {
	t.Parallel()

	node := &Cyclic{Leaf: "end"}
	node.Next = node

	got := walkValue(node)

	if len(got) == 0 {
		t.Fatal("expected settings before the depth cap, got none")
	}

	deepest := 0
	for _, setting := range got {
		deepest = max(deepest, strings.Count(setting.Key, "next."))
	}
	if deepest > maxWalkDepth {
		t.Fatalf("walk reached %d levels, past the %d cap", deepest, maxWalkDepth)
	}
}

func TestConfigEffective_ShouldRenderEveryKeyDefaultsShips(t *testing.T) {
	t.Parallel()

	// defaults.yaml is what every deployment starts from, so each key in it
	// is one an operator can set and therefore one this view owes them.
	// Reflecting over the struct is what makes that hold without a list to
	// maintain; this is what proves it still does.
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewBufferString(defaultconfig)); err != nil {
		t.Fatalf("read the embedded defaults: %v", err)
	}

	rendered := make(map[string]bool, 256)
	for _, setting := range (&Config{}).Effective() {
		rendered[setting.Key] = true
	}

	var missing []string
	for _, key := range v.AllKeys() {
		if !rendered[key] {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		slices.Sort(missing)
		t.Fatalf("defaults.yaml ships keys the effective view never renders: %s",
			strings.Join(missing, ", "))
	}
}

func TestConfigEffective_ShouldTagEverySecretBearingKey(t *testing.T) {
	t.Parallel()

	// A tripwire rather than a restatement of the tags: a field added later
	// under one of these names holds a credential whether or not whoever
	// added it remembered the tag, and this view is where a missed tag
	// discloses it.
	sensitive := map[string]bool{
		"bind_password":     true,
		"client_secret":     true,
		"connection_string": true,
		"cookie_key":        true,
		"password":          true,
		"pin":               true,
		"ssh_key":           true,
	}

	for _, setting := range (&Config{}).Effective() {
		leaf := setting.Key
		if idx := strings.LastIndex(leaf, "."); idx >= 0 {
			leaf = leaf[idx+1:]
		}
		if sensitive[leaf] && !setting.Secret {
			t.Errorf("%s holds a credential but carries no secret:\"true\" tag", setting.Key)
		}
	}
}

func TestConfigEffective_ShouldRedactAConfiguredSecret(t *testing.T) {
	t.Parallel()

	c := &Config{}
	c.AuthConfig.ClientSecret = "s3cret"
	c.DB.Connection = "postgres://user:pw@db/ssoossh"

	rendered := map[string]Setting{}
	for _, setting := range c.Effective() {
		rendered[setting.Key] = setting
	}

	if got := rendered["authentication.client_secret"].Value; got != Redacted {
		t.Errorf("client_secret rendered as %q, want %q", got, Redacted)
	}
	if got := rendered["db.connection_string"].Value; strings.Contains(got, "pw") {
		t.Errorf("connection_string disclosed its password: %q", got)
	}
}

// walkValue runs the walker over any struct pointer, which is what lets the
// table above use small purpose-built types instead of Config.
func walkValue(v any) []Setting {
	out := make([]Setting, 0, 16)
	appendSettings(&out, "", reflect.ValueOf(v).Elem(), false, 0)
	return out
}

// assertSettings compares a walk against the settings it should have
// produced, in order.
func assertSettings(t *testing.T, got, want []Setting) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("got %d settings, want %d\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("setting %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func boolPtr(b bool) *bool { return &b }
