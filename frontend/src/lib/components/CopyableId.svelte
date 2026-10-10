<script lang="ts">
	import Icon from './Icon.svelte';

	// The identifier a page is about, in full where there is room for it.
	//
	// This is the value an operator needs: it is what the notification
	// email, the audit events and the server log lines all carry. So it is
	// shown whole and it is one click to the clipboard. The prefix survives
	// only as the narrow-viewport fallback — a 36-character UUID needs
	// about 260px at this size, which a phone in portrait does not have to
	// spare beside the badges it sits next to.
	//
	// It replaced a five-character stub with the whole value in a `title`
	// tooltip, which is unreachable from a keyboard, invisible on a touch
	// screen, and impossible to select. The tooltip stays for the shortened
	// case, where it is the only way to read the rest.
	interface Props {
		/** The full identifier. What lands on the clipboard. */
		value: string;
		/**
		 * The visible label before the value, and the noun the accessible
		 * name is built from. Short on purpose: the button sits inside a
		 * dialog that already names what this identifies, so "ID" reads
		 * correctly there and "Copy the full ID: <value>" reads correctly
		 * to a screen reader that has just announced the dialog.
		 */
		label?: string;
		/** How many leading characters the shortened form keeps. */
		length?: number;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
	}

	let { value, label = 'ID', length = 5, testid }: Props = $props();

	let copied = $state(false);
	let failed = $state(false);

	// Reset when the identifier changes, so a tick left over from one row
	// does not follow the reader to the next.
	$effect(() => {
		void value;
		copied = false;
		failed = false;
	});

	const short = $derived(value.slice(0, length));

	async function copy() {
		try {
			await navigator.clipboard.writeText(value);
			copied = true;
			failed = false;
		} catch {
			// A refused clipboard used to stay silent, which left the reader
			// pasting whatever was on it before. One quiet line says so and
			// points at the value, which is selectable text.
			copied = false;
			failed = true;
		}
	}
</script>

<span class="inline-flex flex-wrap items-center justify-end gap-x-2">
	<button
		type="button"
		onclick={copy}
		title={value}
		aria-label={copied ? `${label} copied` : `Copy the full ${label}: ${value}`}
		data-testid={testid}
		class="inline-flex min-h-target items-center gap-1 rounded px-2 text-xs text-ink-muted transition hover:bg-surface-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
	>
		{label}
		<!-- Both forms are rendered and one is hidden, rather than measured in
	     script: the answer is "does this row have room", which is a
	     question CSS can answer on its own and JavaScript can only answer
	     after a paint. `sm` is the app's phone-to-tablet fold, and below it
	     this row is wrapping anyway. -->
		<span class="hidden font-mono break-all sm:inline">{value}</span>
		<span class="font-mono sm:hidden">{short}</span>
		<Icon name={copied ? 'copy-check' : 'copy'} size="xs" />
	</button>
	<!-- Always in the document, so the live region exists before it speaks.
	     The value is repeated here as plain text because text inside a
	     button cannot be drag-selected; `select-all` takes it whole in one
	     click. -->
	<span
		role="status"
		class="text-xs text-danger"
		data-testid={testid ? `${testid}-failed` : undefined}
	>
		{#if failed}
			Copy failed. Select it here:
			<span class="font-mono break-all text-ink select-all">{value}</span>
		{/if}
	</span>
</span>
