<script lang="ts">
	import type { Snippet } from 'svelte';
	import Icon from './Icon.svelte';

	// A list with nothing in it, said properly.
	//
	// The screens that had one wrote a single muted sentence — "No users
	// found", "No certificates found matching your search." — which leaves a
	// reader unable to tell the two cases apart that matter: a list that is
	// empty because nothing has happened yet, and a list that is empty
	// because the filters on screen exclude everything in it. The first
	// wants to say what would fill it; the second wants a way back to the
	// full set.
	//
	// The dashboard already did this well ("Nothing yet. Run `ssoossh login`
	// to request a certificate."). This is that, given a shape the other
	// lists can use.
	interface Props {
		/** Icon name from the map in `Icon.svelte`. */
		icon: string;
		/** The state, as a short sentence fragment: "No certificates yet". */
		title: string;
		/** What would fill the list, or what is currently excluding it. */
		children?: Snippet;
		/** One real control: clear the filters, go somewhere, start the thing. */
		action?: Snippet;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
	}

	let { icon, title, children, action, testid }: Props = $props();
</script>

<!-- Centred in the space the rows would have taken, so the page does not
     collapse to a single line when a filter matches nothing. -->
<div
	data-testid={testid}
	class="flex flex-col items-center gap-3 rounded-xl border border-border-subtle bg-surface px-6 py-10 text-center"
>
	<!-- The icon is decoration: the title says the same thing in words, and
	     a reader who cannot see it loses nothing. -->
	<span
		class="flex h-10 w-10 items-center justify-center rounded-full bg-surface-muted text-ink-muted"
	>
		<Icon name={icon} size="md" />
	</span>

	<p class="font-semibold text-ink">{title}</p>

	{#if children}
		<p class="max-w-md text-sm text-ink-muted">{@render children()}</p>
	{/if}

	{#if action}
		<div class="mt-1">{@render action()}</div>
	{/if}
</div>
