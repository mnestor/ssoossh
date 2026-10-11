import type { CertificateType } from '#lib/api/types.js';

/**
 * certificateEvent is what a certificate row is a record of, by type: the
 * words before "2h ago" on the detail line. One map for every list, so the
 * same certificate reads the same on the dashboard, in a person's history
 * and in the admin list; the lists had grown three different wordings.
 *
 * In the "issued" tense because the time beside it is issued_at. PAM and
 * console name the operation rather than the certificate, because that is
 * what the person did: ran sudo, logged in at a console.
 */
export function certificateEvent(type: CertificateType | string | undefined): string {
	switch (type) {
		case 'pam':
			return 'PAM login';
		case 'console':
			return 'console login';
		case 'service':
			return 'service certificate issued';
		default:
			return 'certificate issued';
	}
}
