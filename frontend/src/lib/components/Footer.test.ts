import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';

import type { VersionResponse } from '$lib/api/generated/webtypes';
import Footer from './Footer.svelte';

const release: VersionResponse = {
	version: '0.1.0',
	commit: '7a3f9c1e2d4b6a8c0e2f4a6b8c0d2e4f6a8b0c2d',
	github_url: 'https://github.com/mnestor/ssoossh',
	release_url: 'https://github.com/mnestor/ssoossh/releases/tag/v0.1.0'
};

const untagged: VersionResponse = {
	version: 'development',
	commit: '7a3f9c1e2d4b6a8c0e2f4a6b8c0d2e4f6a8b0c2d',
	github_url: 'https://github.com/mnestor/ssoossh'
};

describe('Footer', () => {
	it('should render nothing when the version is unknown', () => {
		const { container } = render(Footer, { props: { version: null } });
		expect(container.querySelector('footer')).toBeNull();
	});

	it('should show the tagged version with a leading v', () => {
		render(Footer, { props: { version: release } });
		expect(screen.getByRole('link', { name: 'v0.1.0' })).toBeInTheDocument();
	});

	it('should link the tagged version to its release page', () => {
		render(Footer, { props: { version: release } });
		expect(screen.getByRole('link', { name: 'v0.1.0' })).toHaveAttribute(
			'href',
			release.release_url
		);
	});

	it('should link back to the repository', () => {
		render(Footer, { props: { version: release } });
		expect(screen.getByRole('link', { name: 'GitHub' })).toHaveAttribute(
			'href',
			'https://github.com/mnestor/ssoossh'
		);
	});

	it('should link to the repository issue tracker', () => {
		render(Footer, { props: { version: release } });
		expect(screen.getByRole('link', { name: 'Report an issue' })).toHaveAttribute(
			'href',
			'https://github.com/mnestor/ssoossh/issues'
		);
	});

	it('should open outbound links in a new tab without leaking the referrer', () => {
		render(Footer, { props: { version: release } });
		expect(screen.getByRole('link', { name: 'GitHub' })).toHaveAttribute(
			'rel',
			'noopener noreferrer'
		);
	});

	it('should name the untagged build by its short commit', () => {
		render(Footer, { props: { version: untagged } });
		expect(screen.getByText('development (7a3f9c1)')).toBeInTheDocument();
	});

	it('should not link an untagged build to a release page', () => {
		render(Footer, { props: { version: untagged } });
		expect(screen.queryByRole('link', { name: /development/ })).toBeNull();
	});

	it('should leave the version bare when the commit is not a sha', () => {
		render(Footer, { props: { version: { ...untagged, commit: 'commit' } } });
		expect(screen.getByText('development')).toBeInTheDocument();
	});
});
