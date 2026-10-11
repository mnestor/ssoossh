<script lang="ts">
	import { page } from '$app/state';
	import { logout } from '#lib/api/endpoints.js';
	import { startLogin } from '#lib/auth.js';
	import { getBranding } from '#lib/branding.svelte.js';
	import BrandMark from '#lib/components/BrandMark.svelte';
	import Alert from '#lib/components/Alert.svelte';
	import Button from '#lib/components/Button.svelte';
	import ConsentModal from '#lib/components/ConsentModal.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import { isInternalPath } from '#lib/paths.js';
	import { session } from '#lib/session.svelte.js';
	import { describeSignInFailure } from '#lib/signin.js';

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

	// The deployment's own support contact. This is the one page a user
	// locked out of everything else can still reach, so when an operator
	// has named someone, say who rather than "your administrator".
	//
	// The label falls back to the address itself here: read inline in a
	// sentence, "Contact support@example.com" is a complete instruction,
	// which is not true of the footer's nav link (see Footer.svelte).
	const supportEmail = $derived(branding.support_email ?? '');
	const supportLabel = $derived(branding.support_label || supportEmail);

	// Why the last sign-in did not complete, when the callback sent the
	// browser back here instead of finishing it (server/controller/auth.go).
	// The button below is the retry, so the alert needs no button of its own.
	const failure = $derived(describeSignInFailure(page.url.searchParams.get('error')));

	// What the sign-in is for, when the person arrived from a link a
	// terminal or a console printed. Saying so is also the first place to
	// tell a reader who did not start anything that they are about to.
	const purpose = $derived(
		returnTo.startsWith('/approve/')
			? 'Sign in to review the certificate request your terminal opened.'
			: returnTo.startsWith('/c/')
				? 'Sign in to review the console login waiting on that code.'
				: ''
	);

	// Pressed, the button hands the browser to the identity provider, which
	// can take a moment to answer; busy says the press registered.
	let leaving = $state(false);
	function signIn() {
		leaving = true;
		startLogin(returnTo);
	}

	// For someone signed in as the wrong account: end this session and come
	// back here, still carrying where they were going.
	let signingOut = $state(false);
	let signOutError = $state<string | null>(null);
	async function signOutAndRetry() {
		signingOut = true;
		signOutError = null;
		try {
			await logout();
			window.location.assign(`/login?return_to=${encodeURIComponent(returnTo)}`);
		} catch {
			signOutError = 'Sign-out did not complete. You are still signed in. Try again.';
			signingOut = false;
		}
	}
</script>

<svelte:head><title>Sign in · ssoossh</title></svelte:head>

{#if branding.login_notice && !consentAccepted}
	<ConsentModal notice={branding.login_notice} onaccepted={() => (consentAccepted = true)} />
{/if}

<PageShell width="focus" center>
	<div
		data-testid="login-view"
		class="mx-auto flex w-full max-w-[380px] flex-col items-center gap-6 text-center {blocked
			? 'pointer-events-none opacity-50 blur-inert'
			: ''}"
	>
		<BrandMark size={40} strokeWidth={1.6} />

		<div>
			<h1 class="mb-2 font-mono text-2xl leading-tight font-semibold tracking-heading">
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
					You'll sign in with your organization's identity provider.
				</p>
				{#if purpose}
					<p class="mt-2 text-sm text-ink" data-testid="login-purpose">{purpose}</p>
				{/if}
			{/if}
		</div>

		{#if failure}
			<div class="w-full text-left">
				<Alert variant="error" title={failure.title} testid="login-error">{failure.message}</Alert>
			</div>
		{/if}

		{#if session.signedIn}
			<div class="flex w-full flex-col items-center gap-3">
				<!-- Not resolve()d: this is a caller-supplied path, not a route id
				     known at build time. isInternalPath is what keeps it same-origin. -->
				<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
				<a
					class="inline-flex min-h-target w-full items-center justify-center gap-2 rounded-md bg-accent px-4 py-2 text-sm font-medium text-accent-ink transition duration-100 hover:bg-accent-hover"
					href={returnTo}
					data-testid="login-continue"
				>
					Continue
					<Icon name="arrow-right" size="sm" />
				</a>
				<p class="text-xs text-ink-muted">
					Not you?
					<button
						type="button"
						class="inline-flex min-h-target items-center font-medium text-accent hover:underline disabled:opacity-50"
						disabled={signingOut}
						onclick={signOutAndRetry}
						data-testid="login-sign-out">{signingOut ? 'Signing out…' : 'Sign out'}</button
					>
				</p>
				{#if signOutError}
					<p class="text-xs text-danger" role="alert">{signOutError}</p>
				{/if}
			</div>
		{:else}
			<Button testid="sign-in-button" full disabled={blocked} busy={leaving} onclick={signIn}>
				Continue with SSO
				<Icon name="arrow-right" size="sm" />
			</Button>
		{/if}

		{#if supportEmail}
			<p class="text-xs text-ink-muted">
				Trouble signing in? Contact
				<!-- A mailto:, not a route: resolve() validates route ids against
				     this app's route tree and has nothing to check a mail client
				     handoff against. -->
				<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
				<a class="font-medium text-accent hover:underline" href="mailto:{supportEmail}"
					>{supportLabel}</a
				>.
			</p>
		{:else}
			<p class="text-xs text-ink-muted">Trouble signing in? Contact your administrator.</p>
		{/if}
	</div>
</PageShell>
