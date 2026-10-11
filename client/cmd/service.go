package cmd

import "github.com/bep/simplecobra"

func newServiceCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "service",
		short: "Get certificates for unattended jobs, from an enrollment code.",
		long: "Certificates for unattended jobs. A person runs `service enroll` once and " +
			"approves it in the browser, which produces an enrollment code. The job then runs " +
			"`service retrieve` with that code, with no browser, for as long as the enrollment " +
			"lasts.",
		commands: []simplecobra.Commander{
			newServiceEnrollCommand(),
			newServiceRetrieveCommand(),
		},
	}
}
