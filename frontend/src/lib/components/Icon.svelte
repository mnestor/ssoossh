<script lang="ts">
	// The app's whole icon vocabulary, in one place.
	//
	// Every glyph comes from @tabler/icons-svelte and every call site passes
	// a string, so the library appears in exactly this import list and this
	// map. Tabler is drawn on the same 24-unit grid at the same 2px stroke
	// the app was already using, and — unlike a general-purpose set — it
	// carries the nouns this product is about: a certificate, a certificate
	// that no longer counts, a signature, a history.
	//
	// The naming rule the set obeys is noun, then state. `certificate` and
	// `certificate-off` are one drawing in two conditions rather than two
	// unrelated glyphs, and the same holds for `clock` / `clock-cancel` and
	// `filter` / `filter-off`. A state this app has not needed yet already
	// has a glyph waiting, which is what stops the map drifting back into
	// one symbol doing three jobs. See frontend/DESIGN.md, "Iconography".
	import {
		// Certificate types. Four rectilinear objects, so the family reads
		// as one group before any single glyph is recognised.
		IconIdBadge,
		IconTerminal2,
		IconServerCog,
		IconDeviceDesktop,
		// Request status. The clock face is a circle, so `clock-cancel`
		// stays inside the circle family while still saying "time ran out".
		IconHourglassHigh,
		IconLoader2,
		IconCircleCheck,
		IconCircleKey,
		IconCircleX,
		IconClockCancel,
		IconAlertOctagon,
		// Certificate validity, as one object in two conditions.
		IconCertificate,
		IconCertificateOff,
		// Alert severity, on the road-sign ladder: circle, triangle, octagon.
		IconInfoCircle,
		IconAlertTriangle,
		// Filters. `filter-off` states "nothing is filtered" rather than
		// borrowing a wildcard or a destination glyph to imply it.
		IconFilterOff,
		// Primary destinations.
		IconLayoutDashboard,
		IconHistory,
		IconKey,
		IconKeyboard,
		// Admin destinations.
		IconShieldLock,
		IconUsers,
		IconFileCertificate,
		IconAdjustmentsHorizontal,
		IconAddressBook,
		IconBraces,
		IconLogs,
		IconActivityHeartbeat,
		// Account and theme.
		IconUserCog,
		IconSun,
		IconMoon,
		IconDeviceLaptop,
		IconUserCircle,
		IconLogout,
		// Chrome and controls.
		IconMenu2,
		IconLayoutSidebar,
		IconChevronDown,
		IconChevronLeft,
		IconChevronRight,
		IconChevronUp,
		IconArrowRight,
		IconSearch,
		IconX,
		IconCopy,
		IconCheck,
		// Detail rows: a person, and a span of time.
		IconUser,
		IconClock,
		// The only fallback. A certificate type outside the four the schema
		// allows cannot reach the browser, so this marks a contract break
		// rather than decorating one.
		IconHelpCircle
	} from '@tabler/icons-svelte';

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
		// Certificate types
		'id-badge': IconIdBadge,
		'terminal-2': IconTerminal2,
		'server-cog': IconServerCog,
		'device-desktop': IconDeviceDesktop,
		// Request status
		'hourglass-high': IconHourglassHigh,
		'loader-2': IconLoader2,
		'circle-check': IconCircleCheck,
		'circle-key': IconCircleKey,
		'circle-x': IconCircleX,
		'clock-cancel': IconClockCancel,
		'alert-octagon': IconAlertOctagon,
		// Certificate validity
		certificate: IconCertificate,
		'certificate-off': IconCertificateOff,
		// Alert severity
		'info-circle': IconInfoCircle,
		'alert-triangle': IconAlertTriangle,
		// Filters
		'filter-off': IconFilterOff,
		// Primary destinations
		'layout-dashboard': IconLayoutDashboard,
		history: IconHistory,
		key: IconKey,
		keyboard: IconKeyboard,
		// Admin destinations
		'shield-lock': IconShieldLock,
		users: IconUsers,
		'file-certificate': IconFileCertificate,
		'adjustments-horizontal': IconAdjustmentsHorizontal,
		'address-book': IconAddressBook,
		braces: IconBraces,
		logs: IconLogs,
		'activity-heartbeat': IconActivityHeartbeat,
		// Account and theme
		'user-cog': IconUserCog,
		sun: IconSun,
		moon: IconMoon,
		'device-laptop': IconDeviceLaptop,
		'user-circle': IconUserCircle,
		logout: IconLogout,
		// Chrome and controls
		'menu-2': IconMenu2,
		'layout-sidebar': IconLayoutSidebar,
		'chevron-down': IconChevronDown,
		'chevron-left': IconChevronLeft,
		'chevron-right': IconChevronRight,
		'chevron-up': IconChevronUp,
		'arrow-right': IconArrowRight,
		search: IconSearch,
		x: IconX,
		copy: IconCopy,
		check: IconCheck,
		// Detail rows
		user: IconUser,
		clock: IconClock,
		// Fallback
		'help-circle': IconHelpCircle
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
