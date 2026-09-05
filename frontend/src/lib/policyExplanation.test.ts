import { describe, expect, it } from 'vitest';

import { parsePolicyExplanation } from './policyExplanation';

describe('parsePolicyExplanation', () => {
	it('should return null when the input is undefined', () => {
		expect(parsePolicyExplanation(undefined)).toBeNull();
	});

	it('should return null when the input is an empty string', () => {
		expect(parsePolicyExplanation('')).toBeNull();
	});

	it('should return null when the input is not valid JSON', () => {
		expect(parsePolicyExplanation('not json')).toBeNull();
	});

	it('should return null when the parsed JSON is an array rather than an object', () => {
		expect(parsePolicyExplanation('[1,2,3]')).toBeNull();
	});

	it('should return null when the parsed JSON is a bare scalar', () => {
		expect(parsePolicyExplanation('42')).toBeNull();
	});

	it('should return null when ceiling is missing', () => {
		expect(parsePolicyExplanation('{"effective_duration":"8h0m0s"}')).toBeNull();
	});

	it('should return null when effective_duration is missing', () => {
		expect(parsePolicyExplanation('{"ceiling":"8h0m0s"}')).toBeNull();
	});

	it('should parse the ceiling and effective duration with no tier configured', () => {
		const parsed = parsePolicyExplanation(
			'{"v":1,"cert_type":"user","policy_configured":false,"ceiling":"8h0m0s","effective_duration":"8h0m0s"}'
		);
		expect(parsed).toEqual({ ceiling: '8h0m0s', effective_duration: '8h0m0s' });
	});

	it('should parse a winning tier name and condition', () => {
		const parsed = parsePolicyExplanation(
			JSON.stringify({
				ceiling: '24h0m0s',
				effective_duration: '4h0m0s',
				tier: { name: 'on-call', condition: 'member of oncall', max_duration: '4h0m0s' }
			})
		);
		expect(parsed?.tier).toEqual({ name: 'on-call', condition: 'member of oncall' });
	});

	it('should omit tier when the document has no tier field', () => {
		const parsed = parsePolicyExplanation('{"ceiling":"8h0m0s","effective_duration":"8h0m0s"}');
		expect(parsed?.tier).toBeUndefined();
	});

	it('should ignore a malformed tier object missing its condition', () => {
		const parsed = parsePolicyExplanation(
			JSON.stringify({ ceiling: '8h0m0s', effective_duration: '8h0m0s', tier: { name: 'x' } })
		);
		expect(parsed?.tier).toBeUndefined();
	});

	it('should parse a source rule cidr', () => {
		const parsed = parsePolicyExplanation(
			JSON.stringify({
				ceiling: '8h0m0s',
				effective_duration: '1h0m0s',
				source_rule: { cidr: '10.0.0.0/8', max_duration: '1h0m0s' }
			})
		);
		expect(parsed?.source_rule).toEqual({ cidr: '10.0.0.0/8' });
	});

	it('should omit source_rule when the document has none', () => {
		const parsed = parsePolicyExplanation('{"ceiling":"8h0m0s","effective_duration":"8h0m0s"}');
		expect(parsed?.source_rule).toBeUndefined();
	});

	it('should ignore a malformed source_rule object missing its cidr', () => {
		const parsed = parsePolicyExplanation(
			JSON.stringify({
				ceiling: '8h0m0s',
				effective_duration: '8h0m0s',
				source_rule: { max_duration: '1h0m0s' }
			})
		);
		expect(parsed?.source_rule).toBeUndefined();
	});
});
