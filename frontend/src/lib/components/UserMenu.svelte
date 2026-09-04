<script lang="ts">
	import { resolve } from '$app/paths';

	import Icon from './Icon.svelte';
	import { session } from '$lib/session.svelte';

	// The header's identity control: who you are acting as, and the one
	// action that changes it. A menu rather than a bare Sign out button so
	// the header states the identity first — on a tool that issues
	// credentials, "as whom" matters more than "get me out".
	//
	// The identity is spelled out from `sm` up and collapses to a person
	// icon below it. An email address is the widest thing in the header and
	// the only one that is not a control, so on a phone it crowded out the
	// things that are. The accessible name stays the address at every width,
	// so the button never becomes an unlabelled icon.
	interface Props {
		/** The signed-in identity, as shown. */
		label: string;
		/** True while the sign-out call is in flight. */
		busy?: boolean;
		onsignout: () => void;
	}

	let { label, busy = false, onsignout }: Props = $props();

	const isAuditor = $derived(session.user?.is_auditor ?? false);

	let open = $state(false);
	let root = $state<HTMLElement | undefined>(undefined);
	let trigger = $state<HTMLButtonElement | undefined>(undefined);

	// A popover that only closes by re-clicking its trigger is a trap for
	// anyone who opened it by accident, so an outside click or Escape closes
	// it too. Escape returns focus to the trigger rather than dropping it to
	// the document, which would strand a keyboard user at the top of the page.
	$effect(() => {
		if (!open) {
			return;
		}

		function onPointerDown(event: PointerEvent) {
			if (root && !root.contains(event.target as Node)) {
				open = false;
			}
		}

		function onKeyDown(event: KeyboardEvent) {
			if (event.key === 'Escape') {
				open = false;
				trigger?.focus();
			}
		}

		document.addEventListener('pointerdown', onPointerDown);
		document.addEventListener('keydown', onKeyDown);
		return () => {
			document.removeEventListener('pointerdown', onPointerDown);
			document.removeEventListener('keydown', onKeyDown);
		};
	});
</script>

<div bind:this={root} class="relative">
	<button
		bind:this={trigger}
		type="button"
		onclick={() => (open = !open)}
		aria-expanded={open}
		aria-haspopup="menu"
		aria-label={label}
		class="flex items-center gap-1.5 text-[13px] text-ink-muted transition hover:text-ink"
	>
		<span class="sm:hidden"><Icon name="user" size="sm" /></span>
		<span class="hidden max-w-[12rem] truncate sm:inline">{label}</span>
		<Icon name="chevron-down" size="sm" />
	</button>

	{#if open}
		<div
			role="menu"
			aria-label="Account"
			class="absolute right-0 z-40 mt-2 min-w-[10rem] rounded-lg border border-border-subtle bg-surface p-1 shadow-lg"
		>
			<a
				href={resolve('/account')}
				role="menuitem"
				onclick={() => (open = false)}
				class="block w-full rounded px-3 py-2 text-left text-sm transition hover:bg-surface-muted"
			>
				Account
			</a>
			<a
				href={resolve('/preferences')}
				role="menuitem"
				onclick={() => (open = false)}
				class="block w-full rounded px-3 py-2 text-left text-sm transition hover:bg-surface-muted"
			>
				Preferences
			</a>
			{#if isAuditor}
				<hr class="my-1 border-border-subtle" />
				<a
					href={resolve('/admin/users')}
					role="menuitem"
					onclick={() => (open = false)}
					class="block w-full rounded px-3 py-2 text-left text-sm transition hover:bg-surface-muted"
				>
					Admin
				</a>
			{/if}
			<button
				type="button"
				role="menuitem"
				disabled={busy}
				onclick={() => {
					open = false;
					onsignout();
				}}
				class="block w-full rounded px-3 py-2 text-left text-sm transition hover:bg-surface-muted disabled:opacity-50"
			>
				{busy ? 'Signing out…' : 'Sign out'}
			</button>
		</div>
	{/if}
</div>
