<script lang="ts">
	import Icon from './Icon.svelte';

	// A live on/off setting: the change applies on the press, so there is no
	// Save button behind it (form-ux: a toggle applies at once, a checkbox
	// waits for Save). The whole row is the target, as for checkbox rows.
	//
	// The name is the title alone and the description is attached with
	// aria-describedby, so a screen reader reads a short name and then the
	// sentence, rather than both as one long name.
	//
	// On is the "Selected" treatment (DESIGN.md): ink fill, the check glyph,
	// never cobalt. Off is the edge of an operable control, border-control.
	// The knob's side says the state as well, so colour is never the only
	// signal.
	interface Props {
		/** Whether the setting is on. */
		checked: boolean;
		/** The short name of the setting. */
		title: string;
		/** One sentence saying what the setting does. */
		description?: string;
		/** Called with the new value when pressed. */
		onchange: (next: boolean) => void;
		/** True while a change is being saved; presses are ignored meanwhile. */
		busy?: boolean;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
	}

	let { checked, title, description, onchange, busy = false, testid }: Props = $props();

	const id = $props.id();

	function press() {
		if (busy) {
			return;
		}
		onchange(!checked);
	}
</script>

<button
	type="button"
	role="switch"
	aria-checked={checked}
	aria-labelledby="{id}-title"
	aria-describedby={description ? `${id}-description` : undefined}
	aria-busy={busy}
	data-testid={testid}
	onclick={press}
	class="flex min-h-target w-full cursor-pointer items-start gap-3 rounded-md text-left focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
>
	<span
		aria-hidden="true"
		class="mt-0.5 inline-flex h-5 w-9 shrink-0 items-center rounded-md border p-0.5 transition {checked
			? 'border-ink bg-ink'
			: 'border-border-control bg-surface'}"
	>
		<span
			class="inline-flex size-3.5 items-center justify-center rounded-sm transition {checked
				? 'translate-x-4 bg-surface text-ink'
				: 'translate-x-0 bg-ink-muted'}"
		>
			{#if checked}<Icon name="check" size="xs" />{/if}
		</span>
	</span>
	<span class="flex flex-col gap-0.5">
		<span id="{id}-title" class="text-sm font-medium text-ink">{title}</span>
		{#if description}
			<span id="{id}-description" class="text-dense text-ink-muted">{description}</span>
		{/if}
	</span>
</button>
