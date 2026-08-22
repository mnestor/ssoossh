<script lang="ts">
	import Button from './Button.svelte';

	interface Props {
		notice: string;
		onaccepted: () => void;
	}

	let { notice, onaccepted }: Props = $props();
	let open = $state(true);
	let dialogEl = $state<HTMLDialogElement | undefined>(undefined);

	// <dialog> only gets native modal behavior (top-layer stacking, the
	// ::backdrop pseudo-element, focus trapping) via showModal() — an <open>
	// attribute or bare presence in the DOM is not enough. Called imperatively
	// here rather than a declarative `open` attribute for exactly that reason.
	$effect(() => {
		if (open) {
			dialogEl?.showModal();
		}
	});

	// Escape closes a native <dialog> by default, which would let someone
	// bypass acceptance. This is a required notice, not a dismissable one.
	function blockEscape(event: Event) {
		event.preventDefault();
	}

	function handleAccept() {
		open = false;
		dialogEl?.close();
		onaccepted();
	}
</script>

{#if open}
	<!-- No visible title: the notice is each deployment's own approved
	     wording, shown in full and unsummarized, so a generic "Notice"
	     header above it only competes with the text that matters. The
	     heading stays in the accessibility tree to name the dialog. -->
	<dialog
		bind:this={dialogEl}
		oncancel={blockEscape}
		aria-labelledby="consent-notice-heading"
		class="modal-dialog z-50"
	>
		<div
			class="flex w-full max-w-[520px] flex-col gap-4 rounded-xl border border-border-subtle bg-surface p-7 shadow-lg"
		>
			<h2 id="consent-notice-heading" class="sr-only">Notice</h2>

			<!-- A long notice scrolls inside the dialog rather than pushing the
			     Accept button off screen. tabindex makes that scroll reachable
			     from the keyboard in browsers that don't focus overflow
			     containers on their own. -->
			<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
			<div
				role="region"
				aria-labelledby="consent-notice-heading"
				tabindex="0"
				class="max-h-[280px] overflow-y-auto text-sm leading-relaxed whitespace-pre-wrap text-ink"
			>
				{notice}
			</div>

			<div class="flex justify-end">
				<Button onclick={handleAccept}>I Accept</Button>
			</div>
		</div>
	</dialog>
{/if}
