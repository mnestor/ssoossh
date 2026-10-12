<script lang="ts">
	import { tick } from 'svelte';

	import Alert from './Alert.svelte';
	import Button from './Button.svelte';

	// The footer under every "Load more" list: the count line, then the
	// button, or the error alert in the button's place when a page failed.
	//
	// It owns one thing the pages used to each get wrong: where keyboard
	// focus goes when the control the reader pressed goes away. The button
	// unmounts when the last page lands and when a failed page swaps it for
	// the alert, and focus that sat on it fell to the document, so the next
	// Tab started over from the top of the page (WCAG 2.4.3). After every
	// load started here, focus is put back on what now stands in that place:
	//
	// - more to load: the Load more button again;
	// - the last page is in: the count line ("48 shown"), focusable with
	//   tabindex="-1" for exactly this;
	// - the page failed: the alert's Try again button.
	//
	// Only a lost focus is rescued. A reader who moved on while the page was
	// loading (to the search box, a row link) keeps the focus they chose.
	interface Props {
		/** The count line, `describeLoaded` from #lib/listStatus. Empty renders no line. */
		summary: string;
		/** True while the server holds another page. */
		hasMore: boolean;
		/** True while a page is being read; the button shows it. */
		busy: boolean;
		/** Why the last page failed, or null. Replaces the button with an alert. */
		error: string | null;
		/** The alert's title: "Could not load more users". */
		errorTitle: string;
		/** Reads the next page. Return the load's promise so focus waits for it. */
		onmore: () => unknown;
		/** Reads the page that failed again; onmore when not given. */
		onretry?: () => unknown;
		/**
		 * Prefix for the stable selectors: `<testid>-count`,
		 * `<testid>-load-more`, `<testid>-more-error` (and its `-retry`).
		 */
		testid: string;
	}

	let { summary, hasMore, busy, error, errorTitle, onmore, onretry, testid }: Props = $props();

	let root: HTMLDivElement | undefined = $state();
	let count: HTMLParagraphElement | undefined = $state();
	let more: HTMLDivElement | undefined = $state();
	let alert: HTMLDivElement | undefined = $state();

	/**
	 * settle runs a load, waits for the footer to render its outcome, then
	 * puts focus back in the footer if the load took it away.
	 */
	async function settle(load: () => unknown) {
		await load();
		await tick();
		restoreFocus();
	}

	/**
	 * restoreFocus moves focus to what replaced the pressed control, unless
	 * focus is somewhere the reader put it outside this footer.
	 */
	function restoreFocus() {
		if (!root?.isConnected) {
			return;
		}
		const active = document.activeElement;
		const lost = active === null || active === document.body || root.contains(active);
		if (!lost) {
			return;
		}
		focusTarget()?.focus();
	}

	/** focusTarget is the element standing in for the pressed control now. */
	function focusTarget(): HTMLElement | null | undefined {
		if (error) {
			return alert?.querySelector('button');
		}
		if (hasMore) {
			return more?.querySelector('button');
		}
		return count;
	}
</script>

<div bind:this={root} class="flex flex-col gap-3">
	{#if summary}
		<!-- tabindex="-1": not a Tab stop, only where focus lands once the
		     last page is in and the button it sat on is gone. -->
		<p
			bind:this={count}
			tabindex="-1"
			class="text-xs text-ink-muted tabular-nums"
			data-testid="{testid}-count"
		>
			{summary}
		</p>
	{/if}
	{#if error}
		<div bind:this={alert}>
			<Alert
				variant="error"
				title={errorTitle}
				onretry={() => settle(onretry ?? onmore)}
				testid="{testid}-more-error"
			>
				{error}
			</Alert>
		</div>
	{:else if hasMore}
		<div bind:this={more}>
			<Button variant="ghost" {busy} onclick={() => settle(onmore)} testid="{testid}-load-more">
				{busy ? 'Loading…' : 'Load more'}
			</Button>
		</div>
	{/if}
</div>
