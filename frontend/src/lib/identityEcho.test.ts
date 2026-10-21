import { describe, expect, it } from 'vitest';

import type { IdentityEchoPayload } from '#lib/api/types.js';
import {
	echoDownload,
	echoYaml,
	fieldTone,
	readEchoFragment,
	readFailureDetail,
	yamlScalar
} from './identityEcho.js';

/** encode writes a value the way the callback's fragment carries it. */
function encode(value: unknown): string {
	return btoa(JSON.stringify(value)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/** payload is one echo result whose username does not resolve. */
function payload(overrides: Partial<IdentityEchoPayload> = {}): IdentityEchoPayload {
	return {
		claims: { sub: 's', upn: 'alice@example.com', groups: ['ssh-admins'] },
		mapping: { subject: 'sub', username: 'preferred_username', groups: 'groups' },
		fields: [
			{
				field: 'subject',
				claim: 'sub',
				required: true,
				status: 'resolved',
				list: false,
				value: 's'
			},
			{
				field: 'username',
				claim: 'preferred_username',
				required: true,
				status: 'missing',
				list: false
			},
			{
				field: 'groups',
				claim: 'groups',
				required: false,
				status: 'resolved',
				list: true,
				values: ['ssh-admins']
			},
			{ field: 'email', claim: 'email', required: false, status: 'missing', list: false },
			{
				field: 'extra.dept',
				claim: 'https://idp.example.com/dept',
				required: false,
				status: 'missing',
				list: false
			}
		],
		sign_in: { allowed: false, reason: 'OIDC ID token is missing the configured username claim' },
		suggestions: [
			{ field: 'username', claim: 'upn', reason: 'a string', yaml: '' },
			{ field: 'extra', claim: 'upn', reason: 'unmapped', yaml: '' }
		],
		admin_group_candidates: ['ssh-admins'],
		issued_at: '2026-10-21T09:00:00Z',
		...overrides
	};
}

describe('readEchoFragment', () => {
	it.each([
		['', 'none'],
		['#', 'none'],
		['#code=abcDEF_-123', 'code'],
		['#code=', 'unreadable'],
		['#' + encode(payload()), 'payload'],
		['#' + encode({ message: 'no claims here' }), 'unreadable'],
		['#not-base64-json', 'unreadable']
	])('should read %j as %s', (hash, kind) => {
		expect(readEchoFragment(hash).kind).toBe(kind);
	});

	it('should keep the code exactly as the link carried it', () => {
		expect(readEchoFragment('#code=abcDEF_-123')).toEqual({ kind: 'code', code: 'abcDEF_-123' });
	});
});

describe('readFailureDetail', () => {
	it.each([
		['#' + encode({ message: 'oauth2: "invalid_client"' }), 'oauth2: "invalid_client"'],
		['', ''],
		['#garbage', '']
	])('should read %j as %j', (hash, want) => {
		expect(readFailureDetail(hash)).toBe(want);
	});
});

describe('yamlScalar', () => {
	it.each([
		['preferred_username', 'preferred_username'],
		['ssh-admins', 'ssh-admins'],
		['https://idp.example.com/dept', '"https://idp.example.com/dept"'],
		['Domain Admins', '"Domain Admins"'],
		['yes', '"yes"'],
		['123', '"123"'],
		['', '""']
	])('should write %j as %s', (value, want) => {
		expect(yamlScalar(value)).toBe(want);
	});
});

describe('fieldTone', () => {
	it.each([
		[{ status: 'resolved', required: true }, 'granted'],
		[{ status: 'missing', required: true }, 'danger'],
		[{ status: 'wrong_shape', required: false }, 'trimmed'],
		[{ status: 'unconfigured', required: false }, 'muted']
	] as const)('should give %j the %s tone', (field, want) => {
		expect(fieldTone({ field: 'x', list: false, ...field })).toBe(want);
	});
});

describe('echoYaml', () => {
	it('should keep a field that resolves', () => {
		expect(echoYaml(payload(), '')).toContain('    subject: sub\n');
	});

	it('should put the first candidate in place of a field that does not resolve', () => {
		expect(echoYaml(payload(), '')).toContain(
			'    username: upn  # preferred_username does not resolve in this token\n'
		);
	});

	it('should keep a field with no candidate and say it is missing', () => {
		expect(echoYaml(payload(), '')).toContain('    email: email  # not in this token\n');
	});

	it('should quote an extra claim YAML would misread', () => {
		expect(echoYaml(payload(), '')).toContain(
			'      dept: "https://idp.example.com/dept"  # not in this token\n'
		);
	});

	it('should set the chosen admin group', () => {
		expect(echoYaml(payload(), 'ssh-admins')).toContain('admin:\n  require_group: ssh-admins\n');
	});

	it('should leave the admin block out until a group is chosen', () => {
		expect(echoYaml(payload(), '')).not.toContain('\nadmin:');
	});

	it('should not add the extra field suggestions on its own', () => {
		expect(echoYaml(payload(), '')).not.toContain('upn: upn');
	});
});

describe('echoDownload', () => {
	it('should name the file after the echo time', () => {
		expect(echoDownload(payload(), 'yaml').filename).toBe(
			'ssoossh-claims-echo-2026-10-21T090000Z.json'
		);
	});

	it('should carry the whole result and the YAML', async () => {
		const parsed = JSON.parse(await echoDownload(payload(), 'the yaml').blob.text());
		expect(parsed).toMatchObject({
			claims: { upn: 'alice@example.com' },
			suggested_yaml: 'the yaml'
		});
	});
});
