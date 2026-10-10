<script lang="ts">
	import type { Snippet } from 'svelte';

	// One section of a page: a `surface` plane on the `ground`, with no
	// border and no shadow, holding a quiet uppercase heading, an optional
	// line of copy, and the fields themselves.
	//
	// The plane is what says where one group of fields ends and the next
	// begins (DESIGN.md, "Planes"). It says it once: a border, a tint and a
	// shadow on one box would say the same thing three times. Tints inside
	// a section are `surface-muted`, which on the ground would disappear.
	//
	// A real h2 rather than a styled div: a section is a landmark a screen
	// reader navigates by, and it was Card's header that carried that, not
	// its border. SectionLabel stays for the labels *inside* a section,
	// which are not headings and must not become them.
	//
	// One level only. Nothing inside one of these gets a frame of its own:
	// a card inside a card says the inner thing is separate from the page,
	// which is the one thing it is not.
	interface Props {
		/** The section's heading. */
		title: string;
		/** An optional line under it, for what the fields themselves do not say. */
		description?: string;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
		children: Snippet;
	}

	let { title, description, testid, children }: Props = $props();
</script>

<section data-testid={testid} class="rounded-lg bg-surface p-5">
	<h2 class="text-meta font-semibold tracking-label text-ink-muted uppercase">
		{title}
	</h2>
	{#if description}
		<p class="mt-1 text-dense text-ink-muted">{description}</p>
	{/if}
	<div class="mt-3">
		{@render children()}
	</div>
</section>
