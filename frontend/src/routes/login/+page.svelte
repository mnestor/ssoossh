<script lang="ts">
	import { page } from '$app/state';
	import { startLogin } from '$lib/auth';
	import { getBranding } from '$lib/branding.svelte';
	import Button from '$lib/components/Button.svelte';
	import Card from '$lib/components/Card.svelte';
	import ConsentModal from '$lib/components/ConsentModal.svelte';
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
</script>

{#if branding.login_notice && !consentAccepted}
	<ConsentModal notice={branding.login_notice} onaccepted={() => (consentAccepted = true)} />
{/if}

<div class={branding.login_notice && !consentAccepted ? 'pointer-events-none opacity-50' : ''}>
	<Card
		title="Sign in"
		description="ssoossh authorizes SSH certificates against your identity provider."
	>
		{#if session.signedIn}
			<p class="text-sm">
				You are already signed in as <strong>{session.user?.email || session.user?.username}</strong
				>.
			</p>
			<p class="mt-4 text-sm">
				<!-- Not resolve()d: this is a caller-supplied path, not a route id
				     known at build time. isInternalPath is what keeps it same-origin. -->
				<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
				<a class="text-accent hover:underline" href={returnTo}>Continue</a>
			</p>
		{:else}
			<p class="text-sm text-ink-muted">
				You will be sent to your identity provider and returned here once it has confirmed who you
				are. No password is handled by this application.
			</p>
			<div class="mt-5">
				<Button
					disabled={!consentAccepted && !!branding.login_notice}
					onclick={() => startLogin(returnTo)}>Sign in with your identity provider</Button
				>
			</div>
		{/if}
	</Card>
</div>
