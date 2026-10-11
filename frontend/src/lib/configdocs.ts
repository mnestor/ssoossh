/**
 * Where a configuration key is documented. The documentation site's
 * configuration reference (user-docs/src/content/docs/reference/config,
 * generated from the config structs by `make confdocs`) has one page per
 * struct, and each key is a heading on the page of the struct that holds it,
 * anchored by its path below that page with the dots dropped:
 * `cert_options.user.lifetime_policy.default_duration` is on
 * `cert_options/user/` at `#lifetime_policydefault_duration`.
 */
export const configDocsBase = 'https://mnestor.github.io/ssoossh/reference/config';

/**
 * configReferencePages lists the key prefixes that have a page of their own,
 * as dotted paths. A directory's index page is listed by the directory.
 * Kept in step with the site by configdocs.test.ts, which reads the pages.
 */
export const configReferencePages: readonly string[] = [
	'admin',
	'audit',
	'authentication',
	'branding',
	'cert_options',
	'cert_options.console',
	'cert_options.pam',
	'cert_options.service',
	'cert_options.user',
	'db',
	'hsm',
	'http',
	'http.access_logging',
	'http.cert_request_rate_limit',
	'http.tls',
	'ldap',
	'ldap.logging',
	'ldap.sync',
	'logging',
	'mail',
	'mail.logging',
	'mail.smtp',
	'pubsub',
	'queue',
	'ssh_key_agent',
	'version'
];

/**
 * configDocsURL is the reference entry for one dotted key: the page of the
 * longest documented prefix, anchored by the rest. A key with no documented
 * prefix is a top-level key.
 */
export function configDocsURL(key: string): string {
	const parts = key.split('.');
	for (let n = parts.length - 1; n >= 1; n--) {
		const prefix = parts.slice(0, n).join('.');
		if (configReferencePages.includes(prefix)) {
			return `${configDocsBase}/${parts.slice(0, n).join('/')}/#${parts.slice(n).join('')}`;
		}
	}
	return `${configDocsBase}/top-level/#${parts.join('')}`;
}
