import { ApiError } from '$lib/api/client';
import type { CertificateOptions, RequestDetail } from '$lib/api/types';

/**
 * How one certificate option fared against server policy.
 *
 * - `granted`: asked for, and will be in the certificate.
 * - `trimmed`: asked for, and will not be. Server config is the outer bound
 *   on every option, and it trims rather than rejects — so the request still
 *   succeeds, just with less in it than the client asked for. Showing this
 *   before approval is a hard constraint (root CLAUDE.md), because otherwise
 *   a human approves something quietly different from what they were shown.
 * - `added`: present in the granted set without being requested. Nothing
 *   does this today; it is represented so that a policy change which starts
 *   adding options cannot render as silence.
 */
export type OptionStatus = 'granted' | 'trimmed' | 'added';

/** One row of the requested-vs-granted comparison. */
export interface OptionDiffEntry {
	/** Human-facing label, e.g. `permit-pty` or `force-command`. */
	label: string;
	/** Present for options that carry a value rather than just being on. */
	value?: string;
	status: OptionStatus;
}

/** compareLists diffs two sets of named flags into diff entries. */
function compareLists(requested: string[], granted: string[]): OptionDiffEntry[] {
	const grantedSet = new Set(granted);
	const entries: OptionDiffEntry[] = requested.map((label) => ({
		label,
		status: grantedSet.has(label) ? ('granted' as const) : ('trimmed' as const)
	}));

	const requestedSet = new Set(requested);
	for (const label of granted) {
		if (!requestedSet.has(label)) {
			entries.push({ label, status: 'added' });
		}
	}

	return entries;
}

/**
 * extensionDiff compares the requested and granted extension lists.
 */
export function extensionDiff(
	requested: CertificateOptions,
	granted: CertificateOptions
): OptionDiffEntry[] {
	return compareLists(requested.extensions ?? [], granted.extensions ?? []);
}

/**
 * criticalOptionDiff compares the critical options — force-command,
 * source-address, and no-touch-required.
 *
 * force-command and source-address are currently dropped unconditionally at
 * approval (there is no server config to bound them against, see
 * service.Approve), so a client that asks for either will always see them
 * trimmed here. That is the intended display, not a bug: the point is that
 * the human sees the request is not getting what it asked for.
 */
export function criticalOptionDiff(
	requested: CertificateOptions,
	granted: CertificateOptions
): OptionDiffEntry[] {
	const entries: OptionDiffEntry[] = [];

	if (requested.force_command || granted.force_command) {
		entries.push({
			label: 'force-command',
			value: granted.force_command || requested.force_command,
			status: statusFor(!!requested.force_command, !!granted.force_command)
		});
	}

	const requestedAddresses = requested.source_addresses ?? [];
	const grantedAddresses = granted.source_addresses ?? [];
	if (requestedAddresses.length > 0 || grantedAddresses.length > 0) {
		entries.push({
			label: 'source-address',
			value: (grantedAddresses.length > 0 ? grantedAddresses : requestedAddresses).join(', '),
			status: statusFor(requestedAddresses.length > 0, grantedAddresses.length > 0)
		});
	}

	if (requested.no_touch_required || granted.no_touch_required) {
		entries.push({
			label: 'no-touch-required',
			status: statusFor(requested.no_touch_required, granted.no_touch_required)
		});
	}

	return entries;
}

/** statusFor maps a requested/granted pair of booleans onto an OptionStatus. */
function statusFor(wasRequested: boolean, wasGranted: boolean): OptionStatus {
	if (wasGranted) {
		return wasRequested ? 'granted' : 'added';
	}
	return 'trimmed';
}

/** anyTrimmed reports whether the deployment narrowed anything, which is what
 * decides whether the page shows a "this is less than was asked for" notice. */
export function anyTrimmed(entries: OptionDiffEntry[]): boolean {
	return entries.some((entry) => entry.status === 'trimmed');
}

/** Why an approval decision cannot be offered, or null when it can be. */
export type BlockedReason =
	'not-yours' | 'already-resolved' | 'in-progress' | 'no-service-accounts';

/**
 * approvalBlockedReason reports whether the approve/deny buttons should be
 * offered at all.
 *
 * Rendering a button that is guaranteed to fail is worse than rendering an
 * explanation: the server re-checks ownership and status on approve anyway,
 * so this is presentation, not enforcement.
 */
export function approvalBlockedReason(detail: RequestDetail): BlockedReason | null {
	if (!detail.is_owned_by_you) {
		return 'not-yours';
	}
	if (detail.status === 'signing') {
		return 'in-progress';
	}
	if (detail.already_closed || detail.status !== 'pending') {
		return 'already-resolved';
	}
	return null;
}

/** What the approval page should render instead of a request it could not
 * load. Every case here is terminal: a 401 never reaches this, because a
 * signed-out visitor is redirected to /login instead (see auth.goToLogin),
 * so nothing described here offers an action. `kind` is a stable identifier
 * for the e2e browser tier to select on, so it isn't matching against
 * `title`'s prose (see test/e2e/README.md). */
export interface LoadFailure {
	title: string;
	message: string;
	kind: 'forbidden' | 'not-found' | 'unknown';
}

/**
 * describeLoadError turns a failed detail fetch into something a human can
 * act on.
 *
 * 403 gets its own wording because it is not an error in the usual sense:
 * the server binds a request to the first authenticated viewer
 * (service.CertRequestService.bindRequester), so a 403 means someone else
 * already claimed it — most often the user opening a link from a colleague's
 * terminal, which is exactly the confusion worth naming.
 *
 * 401 is deliberately absent: the caller redirects to /login before asking
 * for a description, so "not signed in" is a state this page never renders.
 */
export function describeLoadError(error: unknown): LoadFailure {
	if (error instanceof ApiError) {
		if (error.isForbidden) {
			return {
				title: 'This request belongs to someone else',
				message:
					'It was already opened by another account, and only that account can approve or deny it. Ask whoever started it to approve from their own session.',
				kind: 'forbidden'
			};
		}
		if (error.isNotFound) {
			return {
				title: 'No such request',
				message:
					'It may have expired and been cleaned up, or the link may be incomplete. Run the client again to start a new one.',
				kind: 'not-found'
			};
		}
	}

	return {
		title: 'Could not load this request',
		message: error instanceof Error ? error.message : 'something went wrong',
		kind: 'unknown'
	};
}
