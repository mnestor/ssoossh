<script lang="ts">
	import {
		AlertCircle,
		AlertTriangle,
		Check,
		CheckCircle,
		Copy,
		X,
		XCircle,
		ArrowRight,
		LayoutGrid,
		Link,
		Monitor,
		Moon,
		Sun,
		ChevronDown,
		ChevronLeft,
		ChevronRight,
		ChevronUp,
		Menu,
		Zap,
		User,
		Terminal,
		Cog,
		Server,
		Clock,
		Loader,
		Search,
		Users,
		Award,
		KeyRound,
		SlidersHorizontal,
		BookOpen,
		Code,
		FileText,
		Activity,
		ShieldCheck,
		PanelLeft,
		LogOut,
		Bell
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
		copy: Copy,
		x: X,
		'x-circle': XCircle,
		'arrow-right': ArrowRight,
		'layout-grid': LayoutGrid,
		link: Link,
		monitor: Monitor,
		moon: Moon,
		sun: Sun,
		'chevron-down': ChevronDown,
		'chevron-left': ChevronLeft,
		'chevron-right': ChevronRight,
		'chevron-up': ChevronUp,
		menu: Menu,
		zap: Zap,
		user: User,
		terminal: Terminal,
		cog: Cog,
		server: Server,
		clock: Clock,
		loader: Loader,
		search: Search,
		users: Users,
		award: Award,
		'key-round': KeyRound,
		'sliders-horizontal': SlidersHorizontal,
		'book-open': BookOpen,
		code: Code,
		'file-text': FileText,
		activity: Activity,
		'shield-check': ShieldCheck,
		'panel-left': PanelLeft,
		'log-out': LogOut,
		bell: Bell
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
