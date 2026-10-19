<script lang="ts">
	import FilterChip from './FilterChip.svelte';

	// One named group of filters: "Type: [All][User][PAM]…".
	//
	// Two forms, one decision. From `sm` up it is the name and a row of
	// labelled chips; the name keeps two or three groups on one line from
	// reading as one long row of unrelated buttons. Below `sm` it is a
	// native select with the name as its label: eight chips with their words
	// wrap a filter row onto three lines on a phone, and chips reduced to
	// their glyphs said nothing a reader could read without hovering, which a
	// phone cannot do. A select keeps every option in words at any width.
	//
	// Both forms are rendered and CSS chooses, as CopyableId does: "is there
	// room" is a question CSS answers before the first paint and script only
	// after it. The collapse is decided here, not by a prop, so no list can
	// filter differently from the next one.
	//
	// Every list that filters uses this, so a group's shape is decided once:
	// the label, then the options, held together while the groups beside it
	// wrap as units.
	interface Props {
		/** The question this group answers — "Type", "Status", "Outcome". */
		label: string;
		/** icon may be left out when the words are the whole value (see FilterChip). */
		options: { value: string; label: string; icon?: string }[];
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

	// Ties the select to its visible label. Unique per instance, because a
	// page carries two or three groups, and `for` rather than a wrapping
	// label, which would fold the selected option's text into the select's
	// name ("Type All").
	const selectId = $props.id();
</script>

<div data-testid={testid}>
	<!-- From `sm` up: the name, then a chip per option. -->
	<div
		class="hidden flex-wrap items-center gap-2 sm:flex"
		role="group"
		aria-label="Filter by {label.toLowerCase()}"
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

	<!-- Below `sm`: the same options and words as a native select, which
	     brings the platform's own picker on a phone. `text-base` because it
	     is an input on touch, and iOS zooms into anything smaller. -->
	<div class="flex flex-col gap-1 sm:hidden">
		<label for={selectId} class="text-xs font-semibold text-ink-muted">{label}</label>
		<select
			id={selectId}
			value={selected}
			{disabled}
			onchange={(event) => onselect(event.currentTarget.value)}
			data-testid={testid ? `${testid}-select` : undefined}
			class="min-h-target rounded-md border border-border-control bg-surface px-3 text-base text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50"
		>
			{#each options as option (option.value)}
				<option value={option.value}>{option.label}</option>
			{/each}
		</select>
	</div>
</div>
