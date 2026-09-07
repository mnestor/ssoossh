<script lang="ts">
	// What a list just did, for a reader who cannot see it happen.
	//
	// Every list in the app replaces its rows in place: a search settles, a
	// filter chip is pressed, a page is asked for, and the rows underneath
	// change with nothing said about it. On screen that is obvious. To a
	// screen reader it was silence — no way to tell a filtered list from an
	// empty one from a request that failed, which is WCAG 4.1.3.
	//
	// ApprovalView had carried exactly this region for approval outcomes
	// since it was written; this is that pattern extracted so the lists can
	// have it too.
	//
	// `polite`, not `assertive`: the reader asked for this change, so it
	// waits for a gap rather than cutting across whatever is being read.
	// `atomic`, so a count that goes from 12 to 2 is announced as a whole
	// sentence rather than as the digits that differ.
	interface Props {
		/**
		 * The state of the list, as a sentence. Empty announces nothing,
		 * which is what a list that has not settled yet should do — an
		 * announcement per keystroke of a debounced search is noise, not
		 * information.
		 */
		message: string;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
	}

	let { message, testid = 'list-status' }: Props = $props();
</script>

<div aria-live="polite" aria-atomic="true" data-testid={testid} class="sr-only">{message}</div>
