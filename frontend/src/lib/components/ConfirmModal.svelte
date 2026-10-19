<script lang="ts">
	import type { Snippet } from 'svelte';
	import { scale } from 'svelte/transition';
	import { easeEnter, easeExit, enterMs, exitMs } from '#lib/motion.js';
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
	// audited action that reaches here (see server/service), so a confirm
	// button that could be pressed without one would be a button whose only
	// outcome is a 400. `reasonless` is for an action the server takes no
	// reason for, the live LDAP sync: the dialog then carries only the
	// consequence and the two buttons.
	//
	// `confirmText` is the typed confirmation, for an action that cannot be
	// taken back and whose target is easy to mistake for its neighbour:
	// revoking a fleet key, accepting a changed uid, purging a tombstone. The
	// reader types the exact name or id before the button wakes, so a
	// confirmation meant for one row cannot land on the next one.
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
		/** No reason field, for an action the server records no reason for. */
		reasonless?: boolean;
		/**
		 * The exact text the reader must type before the button is enabled:
		 * a name or an id. Shown in the label, in the mono.
		 */
		confirmText?: string;
		/** What the typed text is, for the label: "the key id", "the name". */
		confirmTextNoun?: string;
		confirmTextTestid?: string;
		/** `danger` for anything that takes access away. */
		variant?: 'primary' | 'danger';
		busy?: boolean;
		/**
		 * Holds the confirming button while the dialog is still finding out
		 * what the reader must know before confirming, such as how many hosts
		 * an action breaks. The body says what it is waiting for.
		 */
		confirmDisabled?: boolean;
		/** Shown above the buttons when the last attempt failed. */
		error?: string | null;
		errorTitle?: string;
		/** Stable selectors for the e2e browser tier — see test/e2e/README.md. */
		reasonTestid?: string;
		confirmTestid?: string;
		/** Called with the trimmed reason (empty when `reasonless`). */
		onconfirm: (reason: string) => void;
		/** Called by Cancel and by Escape, which a native dialog gives free. */
		oncancel: () => void;
		/**
		 * Puts the caret somewhere other than the control that opened the
		 * dialog, for a confirm whose result appears elsewhere on the page.
		 * Called once the dialog has left the page, because until then it is
		 * modal and nothing outside it can take focus. Returns whether it
		 * placed the caret; false falls back to the opener.
		 */
		returnFocus?: () => boolean;
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
		reasonless = false,
		confirmText,
		confirmTextNoun = 'the name',
		confirmTextTestid,
		busy = false,
		confirmDisabled = false,
		error = null,
		errorTitle = 'That did not go through',
		reasonTestid,
		confirmTestid,
		onconfirm,
		oncancel,
		returnFocus
	}: Props = $props();

	// The reason lives here, so a caller opening the dialog with `{#if}`
	// gets an empty field every time: an abandoned draft must not come back
	// attached to the next thing somebody retires.
	let reason = $state('');
	let typed = $state('');
	let dialogEl = $state<HTMLDialogElement | undefined>(undefined);

	// Surrounding blanks are forgiven, since a pasted name often carries one;
	// anything else has to match exactly, case included.
	const typedMatches = $derived(confirmText === undefined || typed.trim() === confirmText);
	const ready = $derived(
		(reasonless || reason.trim().length > 0) && typedMatches && !busy && !confirmDisabled
	);

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
			if (returnFocus?.()) return;
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
		class="flex w-full max-w-md flex-col gap-4 rounded-xl bg-surface p-6"
	>
		<h2 id="confirm-modal-heading" class="text-lg font-semibold text-ink">{title}</h2>

		<div class="text-sm text-ink-muted">{@render children()}</div>

		{#if !reasonless}
			<label class="block">
				<span class="mb-1 block text-xs font-semibold text-ink-muted">{reasonLabel}</span>
				<textarea
					bind:value={reason}
					rows="3"
					disabled={busy}
					data-testid={reasonTestid}
					placeholder={reasonPlaceholder}
					class="w-full rounded-md border border-border-control bg-surface p-2 text-sm disabled:opacity-50"
				></textarea>
				{#if reasonHelp}
					<span class="mt-1 block text-xs text-ink-muted">{reasonHelp}</span>
				{/if}
			</label>
		{/if}

		{#if confirmText !== undefined}
			<label class="block">
				<span class="mb-1 block text-xs font-semibold text-ink-muted">
					Type {confirmTextNoun}
					<span class="font-mono text-ink select-all">{confirmText}</span>
					to confirm
				</span>
				<input
					bind:value={typed}
					type="text"
					autocomplete="off"
					autocapitalize="off"
					spellcheck="false"
					disabled={busy}
					data-testid={confirmTextTestid}
					class="min-h-target w-full rounded-md border border-border-control bg-surface px-3 font-mono text-sm disabled:opacity-50"
				/>
			</label>
		{/if}

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
