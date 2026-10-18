/**
 * The sentence a list's live region announces once it has settled.
 *
 * Every list in the app answers the same two questions after a search or a
 * filter: how many rows there are now, and what narrowed them. Built here rather than written out per
 * page so the five lists announce one shape — a reader who has learned what
 * the certificate list says knows what the user list will say.
 */
export interface ListState {
	/** The singular noun for a row: "certificate", "user", "audit event". */
	noun: string;
	/** How many rows match, across every page. */
	total: number;
	/** True while a request is in flight. */
	loading: boolean;
	/** False until the first response has been rendered. */
	ready: boolean;
	/** The settled search term, when the list has one. */
	query?: string;
}

/**
 * describeList renders the state as one sentence, or as the empty string
 * when there is nothing worth saying yet.
 *
 * Silence during a load is deliberate. A debounced search runs a request per
 * settled term and the region would otherwise announce "Loading" between
 * every one of them, which buries the answer the reader is waiting for
 * underneath the noise of getting it. 4.1.3 asks for the outcome to be
 * announced, not the attempt.
 */
export function describeList(state: ListState): string {
	if (!state.ready || state.loading) {
		return '';
	}

	const term = state.query?.trim();
	const matching = term ? ` matching “${term}”` : '';

	if (state.total === 0) {
		return `No ${plural(state.noun)} found${matching}.`;
	}

	const count = `${state.total} ${state.total === 1 ? state.noun : plural(state.noun)}`;
	return `${count} found${matching}.`;
}

/**
 * plural is deliberately the naive rule.
 *
 * Every noun a list in this app is counting takes a plain -s: certificate,
 * user, service account, audit event, service code. A general pluralizer
 * would be a dependency carried for words nobody is going to add — and if
 * one ever is, this is the single place that has to learn about it.
 */
function plural(noun: string): string {
	return `${noun}s`;
}

/**
 * describeLoaded is the footer line under an admin "Load more" list: how
 * many rows are on screen and, while the server holds more, how many are
 * still to load ("48 shown, 120 older not loaded"). One wording for every
 * such list, so the audit feed and the directory read the same way.
 *
 * remaining names what is left in the list's own order: "older" where the
 * list runs newest first, "more" where it does not (the user directory is
 * sorted by name, so the rest of it is not older). notLoaded at or below
 * zero says nothing, since everything is in.
 *
 * noun, when given, names what is counted ("3 events shown"), for a list
 * whose lines are not one per counted thing: an NSS entry's history pages
 * by audit event but can draw several lines, or none, from one.
 */
export function describeLoaded(
	shown: number,
	notLoaded: number,
	remaining: 'older' | 'more',
	noun?: string
): string {
	const rest = notLoaded > 0 ? `, ${notLoaded} ${remaining} not loaded` : '';
	const what = noun ? ` ${shown === 1 ? noun : plural(noun)}` : '';
	return `${shown}${what} shown${rest}`;
}
