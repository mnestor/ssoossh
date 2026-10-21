<script lang="ts">
	import { COLLAPSE_AFTER, describeClaimValue, lengthOf } from '#lib/claimValue.js';
	import Button from './Button.svelte';

	// One claim's value in the shape its type calls for (DESIGN.md, "Claims
	// echo"). A list is one item per line, so an item containing a comma
	// stays one item; an object is indented JSON; past COLLAPSE_AFTER rows
	// either shows the first ones and a button that opens the rest in place.
	interface Props {
		/** The raw claim value, as decoded from the token. */
		value: unknown;
		/** The claim's name, for the expand button's test id. */
		name: string;
	}

	let { value, name }: Props = $props();

	const shape = $derived(describeClaimValue(value));
	const total = $derived(lengthOf(shape));
	let open = $state(false);
	const shown = $derived(open ? total : COLLAPSE_AFTER);
</script>

{#if shape.kind === 'null'}
	<span class="text-ink-muted">null</span>
{:else if shape.kind === 'empty-string'}
	<span class="text-ink-muted">(empty string)</span>
{:else if shape.kind === 'text'}
	{shape.text}
{:else if shape.kind === 'list'}
	{#if total === 0}
		<span class="text-ink-muted">(empty list)</span>
	{:else}
		<ul class="flex flex-col gap-0.5">
			{#each shape.items.slice(0, shown) as entry, i (i)}
				<li>{entry}</li>
			{/each}
		</ul>
	{/if}
{:else}
	<pre class="font-mono wrap-anywhere whitespace-pre-wrap">{shape.lines
			.slice(0, shown)
			.join('\n')}</pre>
{/if}
{#if total > COLLAPSE_AFTER}
	<div class="mt-1 font-sans">
		<Button
			variant="ghost"
			onclick={() => (open = !open)}
			expanded={open}
			testid="echo-expand-{name}"
		>
			{open ? 'Show fewer' : `Show all ${total}`}
		</Button>
	</div>
{/if}
