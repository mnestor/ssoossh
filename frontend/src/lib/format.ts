/**
 * Formatting helpers. Kept free of Svelte so they can be unit tested
 * directly, and so every screen renders a duration or a timestamp the same
 * way.
 */

/**
 * formatDuration renders a certificate lifetime in the largest units that
 * divide it evenly enough to stay readable — "8h", "1h 30m", "45s".
 *
 * Certificate lifetimes are the number a user actually reasons about ("am I
 * good until the end of the day?"), so this favors legibility over
 * precision: anything under a minute is seconds, and sub-minute remainders
 * are dropped once hours are involved.
 */
export function formatDuration(seconds: number): string {
	if (!Number.isFinite(seconds) || seconds <= 0) {
		return '0s';
	}

	const hours = Math.floor(seconds / 3600);
	const minutes = Math.floor((seconds % 3600) / 60);
	const remainder = Math.floor(seconds % 60);

	if (hours > 0) {
		return minutes > 0 ? `${hours}h ${minutes}m` : `${hours}h`;
	}
	if (minutes > 0) {
		return remainder > 0 ? `${minutes}m ${remainder}s` : `${minutes}m`;
	}
	return `${remainder}s`;
}

/** formatDateTime renders an RFC 3339 timestamp in the viewer's locale and
 * timezone. Invalid input renders as an em dash rather than "Invalid Date". */
export function formatDateTime(value: string): string {
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) {
		return '—';
	}
	return date.toLocaleString(undefined, {
		dateStyle: 'medium',
		timeStyle: 'short'
	});
}

/**
 * expiryLabel describes a certificate's expiry relative to now: "expires in
 * 3h 12m", or "expired" once it has passed.
 *
 * `now` is a parameter so tests can pin it instead of depending on the wall
 * clock.
 */
export function expiryLabel(expiresAt: string, now: Date = new Date()): string {
	const expiry = new Date(expiresAt);
	if (Number.isNaN(expiry.getTime())) {
		return '—';
	}

	const seconds = Math.floor((expiry.getTime() - now.getTime()) / 1000);
	if (seconds <= 0) {
		return 'expired';
	}
	return `expires in ${formatDuration(seconds)}`;
}

/** isExpired reports whether a certificate's validity window has passed,
 * which decides whether the dashboard counts it as active. */
export function isExpired(expiresAt: string, now: Date = new Date()): boolean {
	const expiry = new Date(expiresAt);
	if (Number.isNaN(expiry.getTime())) {
		return true;
	}
	return expiry.getTime() <= now.getTime();
}
