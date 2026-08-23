<script lang="ts">
	import { page } from '$app/state';
	import { startLogin } from '$lib/auth';
	import { getBranding } from '$lib/branding.svelte';
	import BrandMark from '$lib/components/BrandMark.svelte';
	import Button from '$lib/components/Button.svelte';
	import ConsentModal from '$lib/components/ConsentModal.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import { isInternalPath } from '$lib/paths';
	import { session } from '$lib/session.svelte';

	// Where to land after login. Carried in ?return_to= so a redirect here
	// from /approve/<id> comes back to that approval page rather than
	// dumping the user on the dashboard with a link they have to re-open.
	//
	// Only a path is ever forwarded: loginURL refuses anything else, and the
	// server independently re-validates it (server/controller/auth.go's
	// isSafeReturnURL), so this is not the only thing standing between a
	// crafted link and an open redirect.
	const requested = $derived(page.url.searchParams.get('return_to'));
	const returnTo = $derived(isInternalPath(requested) ? requested : '/dashboard');

	const branding = $derived(getBranding());
	let consentAccepted = $state(false);

	// The consent notice blocks rather than sits beside: while it is up the
	// form behind it is blurred, dimmed, and untouchable, so there is no
	// version of this screen where someone signs in without accepting.
	const blocked = $derived(!!branding.login_notice && !consentAccepted);
</script>

<svelte:head><title>Sign in · ssoossh</title></svelte:head>

{#if branding.login_notice && !consentAccepted}
	<ConsentModal notice={branding.login_notice} onaccepted={() => (consentAccepted = true)} />
{/if}

<div
	class="flex w-full flex-1 items-center justify-center {blocked
		? 'pointer-events-none opacity-50 blur-[2px]'
		: ''}"
>
	<div
		data-testid="login-view"
		class="flex w-full max-w-[380px] flex-col items-center gap-[22px] text-center"
	>
		<BrandMark size={40} strokeWidth={1.6} />

		<div>
			<h1 class="mb-2 text-[22px] leading-tight font-bold tracking-[-0.01em]">
				Sign in to ssoossh
			</h1>
			{#if session.signedIn}
				<p class="text-sm text-ink-muted">
					You are already signed in as <strong class="text-ink"
						>{session.user?.email || session.user?.username}</strong
					>.
				</p>
			{:else}
				<p class="text-sm text-ink-muted">
					Certificates are issued through your organization's identity provider.
				</p>
			{/if}
		</div>

		{#if session.signedIn}
			<!-- Not resolve()d: this is a caller-supplied path, not a route id
			     known at build time. isInternalPath is what keeps it same-origin. -->
			<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
			<a class="text-sm font-medium text-accent hover:underline" href={returnTo}>Continue</a>
		{:else}
			<Button testid="sign-in-button" full disabled={blocked} onclick={() => startLogin(returnTo)}>
				Continue with SSO
				<Icon name="arrow-right" size="sm" />
			</Button>
		{/if}

		<p class="text-xs text-ink-muted">Trouble signing in? Contact your administrator.</p>
	</div>
</div>
