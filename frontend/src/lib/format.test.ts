import { describe, expect, it } from 'vitest';

import {
	clockSkewLabel,
	expiryLabel,
	formatDateTime,
	formatDateTimeRange,
	formatDuration,
	isExpired,
	relativeTime,
	remainingLabel
} from './format';

describe('formatDuration', () => {
	const cases: { name: string; seconds: number; want: string }[] = [
		{ name: 'should render whole hours without a minutes part', seconds: 8 * 3600, want: '8h' },
		{
			// An enrollment code's default life is a year. As hours that
			// reads as "8760h", which is the one number the panel exists to
			// state and the one nobody can read.
			name: 'should render a year in months',
			seconds: 365 * 86400,
			want: '12mo 5d'
		},
		{
			name: 'should render whole months without a days part',
			seconds: 90 * 86400,
			want: '3mo'
		},
		{
			name: 'should render days below a month',
			seconds: 14 * 86400,
			want: '14d'
		},
		{
			name: 'should render days and hours when both are present',
			seconds: 2 * 86400 + 5 * 3600,
			want: '2d 5h'
		},
		{
			name: 'should switch from hours to days at twenty-four hours',
			seconds: 24 * 3600,
			want: '1d'
		},
		{
			name: 'should still render hours just under a day',
			seconds: 23 * 3600,
			want: '23h'
		},
		{
			// Two units at most: a code with three months left is not read
			// to the hour.
			name: 'should drop the hours once months are involved',
			seconds: 45 * 86400 + 7 * 3600,
			want: '1mo 15d'
		},
		{
			name: 'should render hours and minutes when both are present',
			seconds: 5400,
			want: '1h 30m'
		},
		{
			name: 'should drop sub-minute remainders once hours are involved',
			seconds: 3661,
			want: '1h 1m'
		},
		{ name: 'should render whole minutes without a seconds part', seconds: 120, want: '2m' },
		{ name: 'should render minutes and seconds below an hour', seconds: 90, want: '1m 30s' },
		{ name: 'should render seconds below a minute', seconds: 45, want: '45s' },
		{ name: 'should render zero as 0s', seconds: 0, want: '0s' },
		{ name: 'should render a negative duration as 0s', seconds: -60, want: '0s' },
		{ name: 'should render a non-finite duration as 0s', seconds: Number.NaN, want: '0s' }
	];

	for (const { name, seconds, want } of cases) {
		it(name, () => {
			expect(formatDuration(seconds)).toBe(want);
		});
	}
});

describe('formatDateTime', () => {
	it('should render an em dash when the timestamp cannot be parsed', () => {
		expect(formatDateTime('not-a-timestamp')).toBe('—');
	});

	it('should render an em dash when the timestamp is empty', () => {
		expect(formatDateTime('')).toBe('—');
	});

	it('should render something other than an em dash for a valid timestamp', () => {
		expect(formatDateTime('2026-08-14T09:00:00Z')).not.toBe('—');
	});

	// Without a zone, a timestamp here cannot be reconciled with the one the
	// client prints on another machine: the two differ by an offset and read
	// as a disagreement.
	it('should name the timezone it rendered the timestamp in', () => {
		expect(formatDateTime('2026-08-14T09:00:00Z')).toMatch(/UTC|GMT|[A-Z]{2,5}$/);
	});
});

describe('formatDateTimeRange', () => {
	it('should join the two ends with an en dash', () => {
		expect(formatDateTimeRange('2026-08-14T09:00:00Z', '2027-08-14T09:00:00Z')).toContain(' – ');
	});

	// The zone belongs to the pair, not to each half: both are rendered in
	// the viewer's own zone, so naming it twice only widens the row.
	it('should name the timezone once, on the closing end', () => {
		const end = '2027-08-14T09:00:00Z';
		// The zone as this environment renders it — "UTC" here, "EDT" or
		// "GMT+5:30" elsewhere — so the count is asserted, not the label.
		const zone = formatDateTime(end).split(' ').pop() as string;
		const range = formatDateTimeRange('2026-08-14T09:00:00Z', end);
		expect(range.split(zone).length - 1).toBe(1);
	});

	it('should fall back to the end alone when the start cannot be parsed', () => {
		expect(formatDateTimeRange('whenever', '2027-08-14T09:00:00Z')).toBe(
			formatDateTime('2027-08-14T09:00:00Z')
		);
	});

	it('should fall back to the start alone when the end cannot be parsed', () => {
		expect(formatDateTimeRange('2026-08-14T09:00:00Z', '')).toBe(
			formatDateTime('2026-08-14T09:00:00Z')
		);
	});
});

describe('remainingLabel', () => {
	const now = new Date('2026-08-14T12:00:00Z');

	it('should render the remaining time without repeating the word expires', () => {
		expect(remainingLabel('2026-08-14T15:30:00Z', now)).toBe('3h 30m left');
	});

	it('should report expired when the validity window has passed', () => {
		expect(remainingLabel('2026-08-14T11:59:59Z', now)).toBe('expired');
	});

	it('should report expired at the exact expiry instant', () => {
		expect(remainingLabel('2026-08-14T12:00:00Z', now)).toBe('expired');
	});

	it('should render an em dash for an unparseable expiry', () => {
		expect(remainingLabel('whenever', now)).toBe('—');
	});
});

describe('expiryLabel', () => {
	const now = new Date('2026-08-14T12:00:00Z');

	it('should describe the remaining time when the certificate is still valid', () => {
		expect(expiryLabel('2026-08-14T15:30:00Z', now)).toBe('expires in 3h 30m');
	});

	it('should report expired when the validity window has passed', () => {
		expect(expiryLabel('2026-08-14T11:59:59Z', now)).toBe('expired');
	});

	it('should report expired at the exact expiry instant', () => {
		expect(expiryLabel('2026-08-14T12:00:00Z', now)).toBe('expired');
	});

	it('should render an em dash for an unparseable expiry', () => {
		expect(expiryLabel('whenever', now)).toBe('—');
	});
});

describe('isExpired', () => {
	const now = new Date('2026-08-14T12:00:00Z');

	it('should report false for a certificate still inside its window', () => {
		expect(isExpired('2026-08-14T12:00:01Z', now)).toBe(false);
	});

	it('should report true for a certificate past its window', () => {
		expect(isExpired('2026-08-14T11:00:00Z', now)).toBe(true);
	});

	// Fails closed: an expiry that cannot be read is not evidence of validity.
	it('should report true for an unparseable expiry', () => {
		expect(isExpired('whenever', now)).toBe(true);
	});
});

describe('clockSkewLabel', () => {
	const reference = '2026-08-14T12:00:00Z';

	it('should report no skew at the tolerance boundary of 30 seconds', () => {
		expect(clockSkewLabel('2026-08-14T11:59:30Z', reference)).toBeNull();
	});

	it('should report no skew just inside the tolerance at 29 seconds', () => {
		expect(clockSkewLabel('2026-08-14T11:59:31Z', reference)).toBeNull();
	});

	it('should report skew once the drift exceeds 30 seconds at 31 seconds', () => {
		expect(clockSkewLabel('2026-08-14T11:59:29Z', reference)).toBe('31s behind server');
	});

	it('should describe a host clock ahead of the server', () => {
		expect(clockSkewLabel('2026-08-14T12:02:00Z', reference)).toBe('2m ahead of server');
	});

	it('should return null when the claimed clock cannot be parsed', () => {
		expect(clockSkewLabel('not-a-timestamp', reference)).toBeNull();
	});

	it('should return null when the reference timestamp cannot be parsed', () => {
		expect(clockSkewLabel(reference, 'not-a-timestamp')).toBeNull();
	});
});

describe('relativeTime', () => {
	const now = new Date('2026-08-22T12:00:00Z');

	it('should say just now when the moment is under a minute old', () => {
		expect(relativeTime('2026-08-22T11:59:30Z', now)).toBe('just now');
	});

	it('should render minutes when under an hour old', () => {
		expect(relativeTime('2026-08-22T11:15:00Z', now)).toBe('45m ago');
	});

	it('should render hours when under a day old', () => {
		expect(relativeTime('2026-08-22T10:00:00Z', now)).toBe('2h ago');
	});

	it('should render days once past twenty-four hours', () => {
		expect(relativeTime('2026-08-19T12:00:00Z', now)).toBe('3d ago');
	});

	it('should render an em dash for an unparseable timestamp', () => {
		expect(relativeTime('not a date', now)).toBe('—');
	});

	it('should treat the exact minute boundary as minutes, not just now', () => {
		expect(relativeTime('2026-08-22T11:59:00Z', now)).toBe('1m ago');
	});

	it('should say just now for a timestamp in the future', () => {
		expect(relativeTime('2026-08-22T12:05:00Z', now)).toBe('just now');
	});
});
