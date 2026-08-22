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
	<dialog
		bind:this={dialogEl}
		oncancel={blockEscape}
		class="fixed inset-0 z-50 flex items-center justify-center p-4 backdrop-blur-sm"
	>
		<div class="w-full max-w-md rounded-lg border border-border-subtle bg-surface shadow-lg">
			<div class="border-b border-border-subtle px-6 py-4">
				<h2 class="text-base font-semibold">Notice</h2>
			</div>

			<div class="px-6 py-4">
				<p class="text-sm whitespace-pre-wrap text-ink">{notice}</p>
			</div>

			<div class="border-t border-border-subtle bg-surface-muted px-6 py-4">
				<Button onclick={handleAccept}>I Accept</Button>
			</div>
		</div>
	</dialog>

	<style>
		dialog::backdrop {
			background-color: rgba(0, 0, 0, 0.5);
		}

		dialog {
			border: none;
			padding: 0;
			background: transparent;
		}
	</style>
{/if}
