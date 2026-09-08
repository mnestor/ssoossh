<script lang="ts">
	import type { Snippet } from 'svelte';
	import { scale } from 'svelte/transition';
	import { easeEnter, easeExit, enterMs, exitMs } from '$lib/motion';
	import Alert from './Alert.svelte';
	import Button from './Button.svelte';

	// The one shape a confirmation takes in this app: a modal over the page,
	// what the action does, a required reason, and the two buttons.
	//
	// Three screens had grown their own. Disabling and re-enabling an
	// account each hand-rolled a `fixed inset-0` overlay — no `role`, no
	// focus trap, and Escape did nothing — while retiring a service code
	// expanded a panel inline halfway down the page, so the control that
	// ended the code sat wherever the sections above it happened to end.
	// The three differ in their wording and their endpoint, which is all a
	// caller should have to supply.
	//
	// The reason is not decoration. The server validates one on every
	// action that reaches here (see the audited actions in server/service),
	// so a confirm button that could be pressed without one would be a
	// button whose only outcome is a 400.
	interface Props {
		/** The dialog's own heading. */
		title: string;
		/** What the action does, read before the reason is typed. */
		children: Snippet;
		/** The label over the reason field. */
		reasonLabel?: string;
		reasonPlaceholder?: string;
		/** An optional line under the field, for who reads the reason later. */
		reasonHelp?: string;
		/** The confirming button, at rest and while the call is in flight. */
		confirmLabel: string;
		busyLabel: string;
		/** `danger` for anything that takes access away. */
		variant?: 'primary' | 'danger';
		busy?: boolean;
		/** Shown above the buttons when the last attempt failed. */
		error?: string | null;
		errorTitle?: string;
		/** Stable selectors for the e2e browser tier — see test/e2e/README.md. */
		reasonTestid?: string;
		confirmTestid?: string;
		/** Called with the trimmed reason. */
		onconfirm: (reason: string) => void;
		/** Called by Cancel and by Escape, which a native dialog gives free. */
		oncancel: () => void;
	}

	let {
		title,
		children,
		reasonLabel = 'Reason (required)',
		reasonPlaceholder = '',
		reasonHelp,
		confirmLabel,
		busyLabel,
		variant = 'danger',
		busy = false,
		error = null,
		errorTitle = 'That did not go through',
		reasonTestid,
		confirmTestid,
		onconfirm,
		oncancel
	}: Props = $props();

	// The reason lives here, so a caller opening the dialog with `{#if}`
	// gets an empty field every time: an abandoned draft must not come back
	// attached to the next thing somebody retires.
	let reason = $state('');
	let dialogEl = $state<HTMLDialogElement | undefined>(undefined);

	const ready = $derived(reason.trim().length > 0 && !busy);

	// <dialog> only gets top-layer stacking, a ::backdrop and a focus trap
	// from showModal(); the open attribute alone gives none of it. Same
	// reason ConsentModal calls it imperatively.
	//
	// The teardown puts the caret back where it came from. A native <dialog>
	// does that for free, but only on close(), and none of the three callers
	// here closes one: each is wrapped in an `{#if}` and dismisses the dialog
	// by unmounting it, so the element and the focus it was holding leave
	// together and the caret lands on <body>. A keyboard reader who retired a
	// code then had to tab the whole page again to get back to where they
	// were, which is the same defect the navigation drawer fixed for itself.
	//
	// Read before showModal() rather than after, because showModal() moves
	// the caret into the dialog and `document.activeElement` would then be
	// the dialog itself.
	$effect(() => {
		const opener = document.activeElement;
		dialogEl?.showModal();
		return () => {
			if (opener instanceof HTMLElement && opener.isConnected) {
				opener.focus();
			}
		};
	});

	// Escape fires the native cancel event. Let it through to the caller so
	// every way out of the dialog lands in the same place.
	function onEscape(event: Event) {
		event.preventDefault();
		if (!busy) {
			oncancel();
		}
	}
</script>

<dialog
	bind:this={dialogEl}
	oncancel={onEscape}
	aria-labelledby="confirm-modal-heading"
	class="modal-dialog z-50"
>
	<!-- The panel animates, the backdrop only arrives: see the ::backdrop
	     note in app.css for why its departure cannot be timed from here. -->
	<div
		in:scale={{ start: 0.96, opacity: 0, duration: enterMs(), easing: easeEnter }}
		out:scale={{ start: 0.96, opacity: 0, duration: exitMs(), easing: easeExit }}
		class="flex w-full max-w-md flex-col gap-4 rounded-xl border border-border-subtle bg-surface p-6 shadow-lg"
	>
		<h2 id="confirm-modal-heading" class="text-lg font-semibold text-ink">{title}</h2>

		<div class="text-sm text-ink-muted">{@render children()}</div>

		<label class="block">
			<span class="mb-1 block text-xs font-semibold text-ink-muted">{reasonLabel}</span>
			<textarea
				bind:value={reason}
				rows="3"
				disabled={busy}
				data-testid={reasonTestid}
				placeholder={reasonPlaceholder}
				class="w-full rounded border border-border-control bg-surface-muted p-2 text-sm disabled:opacity-50"
			></textarea>
			{#if reasonHelp}
				<span class="mt-1 block text-xs text-ink-muted">{reasonHelp}</span>
			{/if}
		</label>

		{#if error}
			<Alert variant="error" title={errorTitle}>{error}</Alert>
		{/if}

		<div class="flex justify-end gap-2">
			<Button variant="ghost" disabled={busy} onclick={oncancel}>Cancel</Button>
			<Button
				{variant}
				testid={confirmTestid}
				disabled={!ready}
				onclick={() => onconfirm(reason.trim())}
			>
				{busy ? busyLabel : confirmLabel}
			</Button>
		</div>
	</div>
</dialog>
