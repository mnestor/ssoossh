<script lang="ts">
	import FilterChip from './FilterChip.svelte';

	// One named group of filter chips: "Type: [All][User][PAM]…".
	//
	// The name matters more than it looks. Below `sm` a chip is its icon
	// alone, so without a word in front of the row there is nothing on
	// screen saying what a tick or a shield is selecting. Above `sm` it is
	// what keeps two or three groups on one line from reading as one long
	// row of unrelated buttons.
	//
	// Every list that filters uses this, so a group's shape is decided once:
	// the label, then the chips, held together on one line while the groups
	// beside it wrap as units.
	interface Props {
		/** The question this group answers — "Type", "Status", "Outcome". */
		label: string;
		options: { value: string; label: string; icon: string }[];
		/** The selected value. Each group carries its own "any" option, so
		 *  there is no separate cleared state to represent. */
		selected: string;
		onselect: (value: string) => void;
		/** Greyed and unpressable while the list behind it is reloading. */
		disabled?: boolean;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
	}

	let { label, options, selected, onselect, disabled = false, testid }: Props = $props();
</script>

<div
	class="flex flex-wrap items-center gap-2"
	role="group"
	aria-label="Filter by {label.toLowerCase()}"
	data-testid={testid}
>
	<span class="text-xs font-semibold text-ink-muted">{label}:</span>
	{#each options as option (option.value)}
		<FilterChip
			label={option.label}
			icon={option.icon}
			selected={selected === option.value}
			{disabled}
			onclick={() => onselect(option.value)}
			testid={testid ? `${testid}-${option.value || 'any'}` : undefined}
		/>
	{/each}
</div>
