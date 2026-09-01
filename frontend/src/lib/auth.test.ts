import { describe, expect, it } from 'vitest';

import { ApiError } from './api/client';
import {
	currentPath,
	errorMessage,
	goToLogin,
	loginPageURL,
	redirectIfUnauthenticated,
	startLogin
} from './auth';

// The navigating helpers call window.location.assign, which jsdom
// implements as a logged no-op ("Not implemented: navigation ...") and
// refuses to let a test spy on. The destination logic is pinned through
// loginPageURL above; the wrappers themselves are exercised for their
// return values and for not throwing.

describe('loginPageURL', () => {
	// The distinction this whole helper exists for: /login is the app's own
	// screen, where a deployment's consent notice is shown, and /auth/login
	// is the jump past it to the identity provider. Anything that needs a
	// signed-in user must land on the former.
	it('should address the app login screen rather than the OIDC entry point', () => {
		expect(loginPageURL('/approve/abc').startsWith('/login')).toBe(true);
	});

	it('should carry a relative path as return_to', () => {
		expect(loginPageURL('/approve/abc')).toBe('/login?return_to=%2Fapprove%2Fabc');
	});

	it('should keep a query string in return_to', () => {
		expect(loginPageURL('/dashboard?modal=abc')).toBe(
			'/login?return_to=%2Fdashboard%3Fmodal%3Dabc'
		);
	});

	it('should omit return_to when none is given', () => {
		expect(loginPageURL()).toBe('/login');
	});

	it('should refuse an absolute URL as return_to', () => {
		expect(loginPageURL('https://evil.example/steal')).toBe('/login');
	});

	it('should refuse a protocol-relative URL as return_to', () => {
		expect(loginPageURL('//evil.example/steal')).toBe('/login');
	});

	it('should refuse an empty return_to', () => {
		expect(loginPageURL('')).toBe('/login');
	});
});

describe('startLogin', () => {
	// The one caller is /login itself, jumping past the consent notice it
	// has just shown. The destination comes from loginURL; here the wrapper
	// only has to hand it to the browser without blowing up.
	it('should hand the browser to the OIDC entry point without throwing', () => {
		expect(() => startLogin('/dashboard')).not.toThrow();
	});
});

describe('goToLogin', () => {
	it('should hand the browser to the app login screen without throwing', () => {
		expect(() => goToLogin('/approve/abc')).not.toThrow();
	});
});

describe('currentPath', () => {
	it('should report the path with its query string', () => {
		history.pushState({}, '', '/logs/me?type=user');

		expect(currentPath()).toBe('/logs/me?type=user');
	});
});

describe('redirectIfUnauthenticated', () => {
	it('should report a redirect when the error is a 401', () => {
		expect(redirectIfUnauthenticated(new ApiError(401, 'not authenticated'))).toBe(true);
	});

	it('should not redirect on a forbidden error', () => {
		expect(redirectIfUnauthenticated(new ApiError(403, 'forbidden'))).toBe(false);
	});

	it('should not redirect on a plain error', () => {
		expect(redirectIfUnauthenticated(new Error('boom'))).toBe(false);
	});
});

describe('errorMessage', () => {
	it('should use the server message from an ApiError', () => {
		expect(errorMessage(new ApiError(403, 'forbidden'))).toBe('forbidden');
	});

	it('should use the message from a plain Error', () => {
		expect(errorMessage(new Error('boom'))).toBe('boom');
	});

	it('should describe a non-Error throw without inventing a message', () => {
		expect(errorMessage('boom')).toBe('something went wrong');
	});
});
