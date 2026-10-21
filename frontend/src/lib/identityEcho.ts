/**
 * The claims echo's browser half: reading what the OIDC callback hands the
 * page in the URL fragment, and building the suggested YAML and the
 * downloadable result. Everything here runs in the browser and nothing is
 * sent anywhere: the server writes no file and keeps no copy, so the page
 * is the only place the result exists.
 */

import type {
	ClaimFieldResolution,
	ClaimSuggestion,
	IdentityEchoFailure,
	IdentityEchoPayload
} from '#lib/api/types.js';

/** decodeBase64Url decodes the base64url the callback encodes with, which
 * has no padding and uses - and _ in place of + and /. */
export function decodeBase64Url(value: string): string {
	const padded = value.replace(/-/g, '+').replace(/_/g, '/');
	const binary = atob(padded + '='.repeat((4 - (padded.length % 4)) % 4));
	return new TextDecoder().decode(Uint8Array.from(binary, (c) => c.charCodeAt(0)));
}

/** decodeFragmentJSON reads a base64url JSON fragment, or throws. */
function decodeFragmentJSON(fragment: string): unknown {
	return JSON.parse(decodeBase64Url(decodeURIComponent(fragment)));
}

/** What a fragment holds. */
export type EchoFragment =
	| { kind: 'none' }
	| { kind: 'code'; code: string }
	| { kind: 'payload'; payload: IdentityEchoPayload }
	| { kind: 'unreadable' };

/**
 * readEchoFragment sorts a location hash into what it carries: a bootstrap
 * link's one-time code (`#code=...`, as `ssoosshd identity echo` prints
 * it), an echo result from the callback, or nothing.
 */
export function readEchoFragment(hash: string): EchoFragment {
	const fragment = hash.replace(/^#/, '');
	if (!fragment) return { kind: 'none' };
	if (fragment.startsWith('code=')) {
		const code = decodeURIComponent(fragment.slice('code='.length)).trim();
		return code ? { kind: 'code', code } : { kind: 'unreadable' };
	}
	try {
		const decoded = decodeFragmentJSON(fragment) as IdentityEchoPayload;
		if (decoded && typeof decoded === 'object' && decoded.claims) {
			return { kind: 'payload', payload: decoded };
		}
	} catch {
		// Falls through to unreadable.
	}
	return { kind: 'unreadable' };
}

/**
 * readFailureDetail reads the error a failed echo carries in its fragment
 * beside ?error=: what the provider or the token check refused. Empty when
 * there is none, or it cannot be read.
 */
export function readFailureDetail(hash: string): string {
	const fragment = hash.replace(/^#/, '');
	if (!fragment) return '';
	try {
		const decoded = decodeFragmentJSON(fragment) as IdentityEchoFailure;
		return typeof decoded?.message === 'string' ? decoded.message : '';
	} catch {
		return '';
	}
}

/** The words for each field status, said the same on every row. */
export const fieldStatusWords: Record<ClaimFieldResolution['status'], string> = {
	resolved: 'Resolved',
	empty: 'Empty',
	missing: 'Missing',
	wrong_shape: 'Wrong shape',
	unconfigured: 'Not configured'
};

/** The tone a field status carries: the login's fate for a required field,
 * a warning for an optional one, nothing at all when unconfigured. */
export function fieldTone(field: ClaimFieldResolution): 'granted' | 'danger' | 'trimmed' | 'muted' {
	if (field.status === 'resolved') return 'granted';
	if (field.status === 'unconfigured') return 'muted';
	return field.required ? 'danger' : 'trimmed';
}

/** YAML words a plain scalar would be read as something other than text. */
const yamlKeywords = new Set(['true', 'false', 'yes', 'no', 'on', 'off', 'null', '~', 'y', 'n']);

/**
 * yamlScalar writes a value as a YAML scalar: bare when it is plain text
 * YAML reads back unchanged, double-quoted otherwise. JSON's string syntax
 * is valid YAML, so JSON.stringify is the quoting.
 */
export function yamlScalar(value: string): string {
	const plain =
		/^[A-Za-z_][A-Za-z0-9_.\-/]*$/.test(value) && !yamlKeywords.has(value.toLowerCase());
	return plain ? value : JSON.stringify(value);
}

/** The reserved fields in config order, and the suggestion field that can
 * stand in for one that does not resolve. */
const reservedFields: Array<{ field: string; candidate?: ClaimSuggestion['field'] }> = [
	{ field: 'subject', candidate: 'subject' },
	{ field: 'username', candidate: 'username' },
	{ field: 'name' },
	{ field: 'groups', candidate: 'groups' },
	{ field: 'other_accounts' },
	{ field: 'service_accounts' },
	{ field: 'email' }
];

/**
 * echoYaml builds the suggested `authentication` and `admin` blocks: every
 * configured field that resolves kept as it is, a field that does not
 * replaced by the first claim the server offered for it (commented, so the
 * change is visible), and admin.require_group set to adminGroup when one
 * was chosen. Suggestions for the extra fields stay where the page lists
 * them: which unread claims to keep is a choice, not a fix.
 */
export function echoYaml(payload: IdentityEchoPayload, adminGroup: string): string {
	const byField = new Map((payload.fields ?? []).map((f) => [f.field, f]));
	const firstCandidate = (field: string) =>
		(payload.suggestions ?? []).find((s) => s.field === field)?.claim;

	const lines = [
		`# Suggested by the ssoossh claims echo, ${payload.issued_at}. Review before use.`,
		'authentication:',
		'  fields:'
	];
	for (const { field, candidate } of reservedFields) {
		const resolution = byField.get(field);
		const configured = resolution?.claim ?? '';
		if (resolution?.status === 'resolved') {
			lines.push(`    ${field}: ${yamlScalar(configured)}`);
			continue;
		}
		const replacement = candidate ? firstCandidate(candidate) : undefined;
		if (replacement) {
			const was = configured
				? `${configured} does not resolve in this token`
				: 'was not configured';
			lines.push(`    ${field}: ${yamlScalar(replacement)}  # ${was}`);
		} else if (configured) {
			lines.push(`    ${field}: ${yamlScalar(configured)}  # not in this token`);
		}
	}

	const extras = (payload.fields ?? []).filter((f) => f.field.startsWith('extra.') && f.claim);
	if (extras.length > 0) {
		lines.push('    extra:');
		for (const f of extras) {
			const note = f.status === 'resolved' ? '' : '  # not in this token';
			lines.push(
				`      ${yamlScalar(f.field.slice('extra.'.length))}: ${yamlScalar(f.claim ?? '')}${note}`
			);
		}
	}

	if (adminGroup) {
		lines.push('admin:', `  require_group: ${yamlScalar(adminGroup)}`);
	} else {
		lines.push('# admin.require_group: choose one of your groups above to add it here.');
	}
	return lines.join('\n') + '\n';
}

/**
 * echoDownload is the whole result as a file, built here: the payload as
 * the page received it, plus the YAML as it stands. Nothing is fetched.
 */
export function echoDownload(
	payload: IdentityEchoPayload,
	yaml: string
): { filename: string; blob: Blob } {
	const stamp = payload.issued_at.replace(/[:]/g, '').replace(/\..*$/, '');
	const body = JSON.stringify({ ...payload, suggested_yaml: yaml }, null, 2) + '\n';
	return {
		filename: `ssoossh-claims-echo-${stamp}.json`,
		blob: new Blob([body], { type: 'application/json' })
	};
}

/**
 * saveFile hands blob to the browser as a download under filename, through
 * an object URL that is released straight after.
 */
export function saveFile(filename: string, blob: Blob): void {
	const url = URL.createObjectURL(blob);
	const link = document.createElement('a');
	link.href = url;
	link.download = filename;
	document.body.append(link);
	link.click();
	link.remove();
	URL.revokeObjectURL(url);
}
