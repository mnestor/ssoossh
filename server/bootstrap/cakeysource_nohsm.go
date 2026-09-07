//go:build !hsm

package bootstrap

import (
	"errors"

	"github.com/mnestor/ssoossh/server/signer"
)

// newHSMCAKeySource reports that this build cannot load a PKCS#11 module.
//
// The default build is CGO_ENABLED=0 and statically linked, which is only
// possible because crypto11 is not in it. An HSM is still reachable two
// ways, and the first is not a compromise: `ssh-add -s <module>` loads the
// token into an ssh-agent, the key still never leaves the hardware, and the
// vendor's C++ module runs in the agent's address space instead of the
// signer's.
//
// not covered: a one-line refusal with no branches. The behaviour that
// matters -- that ssoosshd refuses rather than silently signing with some
// other key -- is covered by the config exclusivity tests.
func (a *app) newHSMCAKeySource() (signer.CAKeySource, error) {
	return nil, errors.New("hsm is configured but this build has no PKCS#11 support: " +
		"reach the token through an ssh-agent (`ssh-add -s <module>`, then configure ssh_key_agent), " +
		"or run the signer from an hsm-tagged build on the machine the token is attached to")
}
