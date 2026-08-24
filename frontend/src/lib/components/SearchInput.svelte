<script lang="ts">
	import Icon from './Icon.svelte';

	// A debounced search box for the paged admin and auditor lists. Reporting
	// on a timer rather than on every keystroke is what keeps a list from
	// issuing a query per character; reporting only when the settled term
	// actually changed is what keeps a stray space from re-running the same
	// search.
	//
	// `value` seeds the box and is not watched afterwards: the box owns what
	// is typed in it, and a page that needs to reset the term remounts it with
	// a key. Anything else would fight the user mid-keystroke.
	interface Props {
		/** Accessible name for the box, e.g. "Search users". */
		label: string;
		/** The term to start with, usually read back from the URL. */
		value?: string;
		placeholder?: string;
		/** How long the typing has to settle before the term is reported. */
		delay?: number;
		/** Called with the trimmed term whenever it settles on something new. */
		onsearch: (query: string) => void;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
	}

	let {
		label,
		value = '',
		placeholder = 'Search…',
		delay = 250,
		onsearch,
		testid
	}: Props = $props();

	// svelte-ignore state_referenced_locally
	let term = $state(value);

	// What the caller has already been told. Not $state: nothing renders from
	// it, and it must not participate in reactivity — it is the record of a
	// side effect, read only to decide whether to repeat one.
	// svelte-ignore state_referenced_locally
	let reported = value.trim();
	let timer: ReturnType<typeof setTimeout> | undefined;

	/**
	 * report cancels any pending debounce and hands the caller `next`, unless
	 * that is what they were last told.
	 */
	function report(next: string) {
		clearTimeout(timer);
		timer = undefined;
		if (next === reported) {
			return;
		}
		reported = next;
		onsearch(next);
	}

	/** schedule restarts the debounce against whatever is in the box now. */
	function schedule() {
		clearTimeout(timer);
		timer = setTimeout(() => report(term.trim()), delay);
	}

	/** submit reports immediately, for a user who pressed Enter to say "now". */
	function submit(event: KeyboardEvent) {
		if (event.key === 'Enter') {
			event.preventDefault();
			report(term.trim());
		}
	}

	/** clear empties the box and reports it without waiting out the debounce. */
	function clear() {
		term = '';
		report('');
	}

	// A pending debounce outliving the page would call back into a component
	// that is gone.
	$effect(() => () => clearTimeout(timer));
</script>

<div class="relative flex items-center">
	<span class="pointer-events-none absolute left-3 text-ink-muted">
		<Icon name="search" size="sm" />
	</span>
	<input
		type="search"
		aria-label={label}
		{placeholder}
		data-testid={testid}
		bind:value={term}
		oninput={schedule}
		onkeydown={submit}
		class="w-full rounded-md border border-border-subtle bg-surface py-2 pr-9 pl-9 text-sm text-ink placeholder:text-ink-muted focus:border-accent focus:outline-none"
	/>
	{#if term !== ''}
		<button
			type="button"
			aria-label="Clear search"
			onclick={clear}
			class="absolute right-2 rounded p-1 text-ink-muted transition hover:bg-surface-muted hover:text-ink"
		>
			<Icon name="x" size="sm" />
		</button>
	{/if}
</div>
