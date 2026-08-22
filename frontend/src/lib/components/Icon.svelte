<script lang="ts">
	import {
		AlertCircle,
		AlertTriangle,
		Check,
		CheckCircle,
		X,
		XCircle,
		ChevronDown,
		ChevronLeft,
		ChevronRight,
		Menu,
		Zap,
		User,
		Terminal,
		Cog,
		Server,
		Clock,
		Loader
	} from '@lucide/svelte';

	interface Props {
		name: string;
		size?: 'xs' | 'sm' | 'md' | 'lg' | 'xl';
		ariaLabel?: string;
		class?: string;
	}

	let { name, size = 'md', ariaLabel, class: cls = '' }: Props = $props();

	const sizeMap = {
		xs: 12,
		sm: 16,
		md: 20,
		lg: 24,
		xl: 32
	};

	const iconComponents: Record<string, any> = {
		'alert-circle': AlertCircle,
		'alert-triangle': AlertTriangle,
		check: Check,
		'check-circle': CheckCircle,
		x: X,
		'x-circle': XCircle,
		'chevron-down': ChevronDown,
		'chevron-left': ChevronLeft,
		'chevron-right': ChevronRight,
		menu: Menu,
		zap: Zap,
		user: User,
		terminal: Terminal,
		cog: Cog,
		server: Server,
		clock: Clock,
		loader: Loader
	};

	const IconComponent = $derived(iconComponents[name]);
	const iconSize = $derived(sizeMap[size]);
</script>

{#if IconComponent}
	<IconComponent
		size={iconSize}
		aria-label={ariaLabel}
		class={`flex-shrink-0 ${cls}`}
		aria-hidden={!ariaLabel}
	/>
{:else}
	<div class="flex h-5 w-5 items-center justify-center bg-red-200 text-xs font-bold text-red-600">
		?
	</div>
{/if}
