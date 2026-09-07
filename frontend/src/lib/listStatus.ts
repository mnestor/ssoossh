/**
 * The sentence a list's live region announces once it has settled.
 *
 * Every list in the app answers the same three questions after a search, a
 * filter or a page: how many rows there are now, which window of them is on
 * screen, and what narrowed them. Built here rather than written out per
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
	/** 1-based page number, when the list is paged. */
	page?: number;
	/** How many pages there are, when the list is paged. */
	pageCount?: number;
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
	const sentence = `${count} found${matching}.`;

	// The page position only earns its place when there is more than one
	// page. On a single-page list "Page 1 of 1" is three words that say
	// nothing the count did not already say.
	if (state.pageCount !== undefined && state.pageCount > 1 && state.page !== undefined) {
		return `${sentence} Page ${state.page} of ${state.pageCount}.`;
	}
	return sentence;
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
