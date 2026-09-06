/**
 * Formatting helpers. Kept free of Svelte so they can be unit tested
 * directly, and so every screen renders a duration or a timestamp the same
 * way.
 */

/** Seconds in a day, and in the nominal month used for the coarsest unit. */
const SECONDS_PER_DAY = 86_400;
const DAYS_PER_MONTH = 30;

/**
 * formatDuration renders a certificate lifetime in the largest units that
 * divide it evenly enough to stay readable — "3mo 5d", "14d", "8h",
 * "1h 30m", "45s".
 *
 * Certificate lifetimes are the number a user actually reasons about ("am I
 * good until the end of the day?"), so this favors legibility over
 * precision: each tier shows at most two units and drops the rest, since a
 * code with three months left is not read to the minute.
 *
 * The long tiers exist because the long-lived things here really are long:
 * an enrollment code's default life is a year, and rendering that as
 * "8760h" made the one number the page exists to state unreadable. A month
 * is a nominal 30 days rather than a calendar month — the input is a
 * duration, not a pair of dates, so there is no calendar to be exact
 * against, and the label is approximate by design.
 */
export function formatDuration(seconds: number): string {
	if (!Number.isFinite(seconds) || seconds <= 0) {
		return '0s';
	}

	const days = Math.floor(seconds / SECONDS_PER_DAY);
	if (days >= DAYS_PER_MONTH) {
		const months = Math.floor(days / DAYS_PER_MONTH);
		const remainingDays = days % DAYS_PER_MONTH;
		return remainingDays > 0 ? `${months}mo ${remainingDays}d` : `${months}mo`;
	}
	if (days > 0) {
		const remainingHours = Math.floor((seconds % SECONDS_PER_DAY) / 3600);
		return remainingHours > 0 ? `${days}d ${remainingHours}h` : `${days}d`;
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

/**
 * formatDateTime renders an RFC 3339 timestamp in the viewer's locale and
 * timezone. Invalid input renders as an em dash rather than "Invalid Date".
 *
 * The zone is named rather than implied. These are audit timestamps that get
 * compared against something outside the browser — `ssh-keygen -L`, a
 * client's "valid until 06:24", a log line on the host — and the machine
 * printing that other line is often in a different zone than the machine
 * reading this one. Without the zone the two just disagree, and the reader
 * has no way to tell a timezone offset from a wrong timestamp.
 */
export function formatDateTime(value: string): string {
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) {
		return '—';
	}
	// Spelled out as components rather than dateStyle/timeStyle: Intl rejects
	// timeZoneName alongside either style, and the zone is the point. The
	// components chosen reproduce what medium/short rendered before it.
	return date.toLocaleString(undefined, {
		year: 'numeric',
		month: 'short',
		day: 'numeric',
		hour: 'numeric',
		minute: '2-digit',
		timeZoneName: 'short'
	});
}

/**
 * formatDateTimeRange renders a validity window as one string — "Sep 6,
 * 2026, 12:31 PM – Sep 6, 2027, 12:31 PM EDT".
 *
 * Both ends keep their time, but the zone is named once, at the end: both
 * halves are rendered in the viewer's own zone, so repeating it widens the
 * row without adding a fact. A window is one thing to a reader ("how long
 * does this code live?"), and splitting it across two rows made them do the
 * subtraction themselves.
 *
 * An unparseable half falls back to the other one, so a bad timestamp costs
 * the range, not the row.
 */
export function formatDateTimeRange(start: string, end: string): string {
	const from = new Date(start);
	if (Number.isNaN(from.getTime())) {
		return formatDateTime(end);
	}
	if (Number.isNaN(new Date(end).getTime())) {
		return formatDateTime(start);
	}

	const opening = from.toLocaleString(undefined, {
		year: 'numeric',
		month: 'short',
		day: 'numeric',
		hour: 'numeric',
		minute: '2-digit'
	});
	return `${opening} – ${formatDateTime(end)}`;
}

/**
 * remainingLabel says how much of a validity window is left — "12mo 4d
 * left", or "expired" once it has passed.
 *
 * The parenthetical form of expiryLabel: in a row that already names both
 * ends of the window, "expires in 12mo 4d" repeats what the row just said.
 */
export function remainingLabel(expiresAt: string, now: Date = new Date()): string {
	const expiry = new Date(expiresAt);
	if (Number.isNaN(expiry.getTime())) {
		return '—';
	}

	const seconds = Math.floor((expiry.getTime() - now.getTime()) / 1000);
	if (seconds <= 0) {
		return 'expired';
	}
	return `${formatDuration(seconds)} left`;
}

/**
 * clockSkewLabel compares a claimed clock — e.g. a PAM module's
 * client_time — against a reference timestamp, normally the server's own
 * created_at, and describes the drift: "31s behind server". Returns null
 * within a 30 second tolerance, or when either timestamp fails to parse.
 * Ordinary clock drift is not worth a row; only a gap big enough to
 * suggest the host's clock (or the request itself) is wrong is.
 */
export function clockSkewLabel(clientTime: string, reference: string): string | null {
	const client = new Date(clientTime);
	const ref = new Date(reference);
	if (Number.isNaN(client.getTime()) || Number.isNaN(ref.getTime())) {
		return null;
	}

	const driftSeconds = Math.round((ref.getTime() - client.getTime()) / 1000);
	if (Math.abs(driftSeconds) <= 30) {
		return null;
	}

	const direction = driftSeconds > 0 ? 'behind server' : 'ahead of server';
	return `${formatDuration(Math.abs(driftSeconds))} ${direction}`;
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

/**
 * relativeTime renders how long ago a timestamp was, in the coarse units a
 * list row wants — "2h ago", "3d ago", "just now".
 *
 * Deliberately coarser than expiryLabel: a history row is scanned, not read,
 * and "requested 2h 14m ago" carries no more meaning than "2h ago". `now` is
 * a parameter so tests can pin it instead of depending on the wall clock.
 */
export function relativeTime(value: string, now: Date = new Date()): string {
	const then = new Date(value);
	if (Number.isNaN(then.getTime())) {
		return '—';
	}

	const seconds = Math.floor((now.getTime() - then.getTime()) / 1000);
	if (seconds < 60) {
		return 'just now';
	}

	const minutes = Math.floor(seconds / 60);
	if (minutes < 60) {
		return `${minutes}m ago`;
	}

	const hours = Math.floor(minutes / 60);
	if (hours < 24) {
		return `${hours}h ago`;
	}

	return `${Math.floor(hours / 24)}d ago`;
}
