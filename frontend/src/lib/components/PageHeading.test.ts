import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { createRawSnippet } from 'svelte';
import { describe, expect, it, vi } from 'vitest';

import PageHeading from './PageHeading.svelte';

const sub = createRawSnippet(() => ({
	render: () => '<span>Directory of all users</span>'
}));

const action = createRawSnippet(() => ({
	render: () => '<a href="/logs/me">View all history</a>'
}));

describe('PageHeading', () => {
	it('should render the title as the page heading', () => {
		render(PageHeading, { eyebrow: 'Activity', title: 'Recent decisions' });
		expect(screen.getByRole('heading', { level: 1, name: 'Recent decisions' })).toBeInTheDocument();
	});

	it('should render the eyebrow above the title', () => {
		render(PageHeading, { eyebrow: 'Activity', title: 'Recent decisions' });
		expect(screen.getByText('Activity')).toBeInTheDocument();
	});

	// The three admin table pages opened with a bare h1 and a paragraph
	// rather than this component, which is why the sub line exists.
	it('should render a sub line under the title when one is given', () => {
		render(PageHeading, { eyebrow: 'Admin', title: 'Users', sub });
		expect(screen.getByText('Directory of all users')).toBeInTheDocument();
	});

	it('should render no sub line when none is given', () => {
		const { container } = render(PageHeading, { eyebrow: 'Admin', title: 'Users' });
		expect(container.querySelector('p')).toBeNull();
	});

	// The chip was the same nine-class string copied onto three pages as an
	// <a> and a fourth as a <button>, each cancelling the shell's gap with
	// its own negative margin.
	it('should render a back chip as a link when it is given an href', () => {
		render(PageHeading, {
			eyebrow: 'Certificate',
			title: 'Details',
			back: { href: '/logs/me', label: 'Certificate history' }
		});
		expect(screen.getByRole('link', { name: /Certificate history/ })).toHaveAttribute(
			'href',
			'/logs/me'
		);
	});

	// One list opens an account without changing route, so its chip has to
	// be a button rather than a link to nowhere.
	it('should render a back chip as a button when it is given an onclick', async () => {
		const onclick = vi.fn();
		render(PageHeading, {
			eyebrow: 'Service account',
			title: 'svc-deploy',
			back: { onclick, label: 'All service accounts' }
		});

		await userEvent.click(screen.getByRole('button', { name: /All service accounts/ }));

		expect(onclick).toHaveBeenCalledOnce();
	});

	it('should render no back chip when none is given', () => {
		render(PageHeading, { eyebrow: 'Activity', title: 'Recent decisions' });
		expect(screen.queryByRole('link')).toBeNull();
	});

	it('should render a trailing action when one is given', () => {
		render(PageHeading, { eyebrow: 'Activity', title: 'Recent decisions', action });
		expect(screen.getByRole('link', { name: 'View all history' })).toBeInTheDocument();
	});
});
