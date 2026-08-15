<script lang="ts">
	import type { OptionDiffEntry } from '$lib/approval';

	interface Props {
		entries: OptionDiffEntry[];
		/** Shown when the request asked for nothing in this category. */
		emptyLabel: string;
	}

	let { entries, emptyLabel }: Props = $props();

	// Trimmed entries are struck through and labelled rather than hidden.
	// Hiding them would defeat the point: the human is being asked to
	// authorize the granted set, and can only judge it against what was
	// asked for.
	const styles = {
		granted: 'bg-granted-surface text-granted',
		trimmed: 'bg-trimmed-surface text-trimmed line-through',
		added: 'bg-surface-muted text-ink-muted'
	};

	const notes = {
		granted: '',
		trimmed: 'not permitted by this server',
		added: 'added by server policy'
	};
</script>

{#if entries.length === 0}
	<p class="text-sm text-ink-muted">{emptyLabel}</p>
{:else}
	<ul class="space-y-2">
		{#each entries as entry (entry.label)}
			<li class="flex flex-wrap items-center gap-2 text-sm">
				<code class="rounded px-2 py-0.5 font-mono text-xs {styles[entry.status]}">
					{entry.label}{#if entry.value}: {entry.value}{/if}
				</code>
				{#if notes[entry.status]}
					<span class="text-xs text-ink-muted">{notes[entry.status]}</span>
				{/if}
			</li>
		{/each}
	</ul>
{/if}
