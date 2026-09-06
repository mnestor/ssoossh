package config

// Reserved values of version.mode. Any value that is neither of these is
// taken literally as the version string to display (see VersionConfig.Mode).
const (
	// VersionShow reports the real version, commit, and release URL.
	VersionShow = "show"
	// VersionHide reports nothing identifying: every field comes back empty.
	VersionHide = "hide"
)

// VersionConfig configures what the unauthenticated /api/version endpoint
// discloses about the running build. The endpoint feeds the web UI footer,
// but it is reachable by anyone, so a build's exact version and commit are
// handed to whoever wants to match the deployment against known issues. This
// lets an operator trade that disclosure away.
type VersionConfig struct {
	// Mode selects what the endpoint reveals:
	//
	//   "show"          the real version, commit, and release URL (default)
	//   "hide"          nothing; every field is empty
	//   any other value that string, reported as the version, with no
	//                   commit, repository, or release link that would give
	//                   the real build away
	//
	// The two words "show" and "hide" are therefore reserved and cannot
	// themselves be used as a fake version string.
	Mode string `mapstructure:"mode" default:"show"`
}
