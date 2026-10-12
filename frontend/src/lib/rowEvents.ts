import type { CertificateType } from '#lib/api/types.js';

/**
 * pamServiceWords names the PAM services pam_ssoossh is documented for
 * (sudo, su, a password-less sshd stack, console login), in the words a
 * person would use for what they did.
 */
const pamServiceWords: Record<string, string> = {
	sudo: 'sudo',
	su: 'su',
	sshd: 'SSH login',
	login: 'login'
};

/**
 * certificateEvent is what a certificate row is a record of, by type: the
 * words before "2h ago" on the detail line. One map for every list, so the
 * same certificate reads the same on the dashboard, in a person's history
 * and in the admin list; the lists had grown three different wordings.
 *
 * In the "issued" tense because the time beside it is issued_at. PAM and
 * console name the operation rather than the certificate, because that is
 * what the person did: ran sudo, logged in at a console.
 *
 * A PAM row names the PAM service the module reported (reportedPamService)
 * when it is one pam_ssoossh is documented for, marked "(reported)": the
 * value comes from an unauthenticated caller and is a claim, never a fact.
 * Any other value, or none, reads "PAM login", so a list row never shows
 * free text a caller chose; the certificate page shows the raw value.
 *
 * Every known type is named in words, because this line is where a row
 * says its type: TypeBadge's glyph beside it is a mark, not a name, and a
 * glyph that needs a hover to be understood has failed (DESIGN.md,
 * Iconography). The default stays unnamed on purpose: it also covers a
 * missing or unknown type, which must not be labelled "user".
 */
export function certificateEvent(
	type: CertificateType | string | undefined,
	reportedPamService?: string
): string {
	switch (type) {
		case 'user':
			return 'user certificate issued';
		case 'pam': {
			// Own keys only: a reported "toString" must not find Object's.
			const words =
				reportedPamService && Object.hasOwn(pamServiceWords, reportedPamService)
					? pamServiceWords[reportedPamService]
					: undefined;
			return words ? `${words} (reported)` : 'PAM login';
		}
		case 'console':
			return 'console login';
		case 'service':
			return 'service certificate issued';
		default:
			return 'certificate issued';
	}
}
