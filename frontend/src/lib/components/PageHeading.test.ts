import { render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { describe, expect, it } from 'vitest';

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

	it('should render a trailing action when one is given', () => {
		render(PageHeading, { eyebrow: 'Activity', title: 'Recent decisions', action });
		expect(screen.getByRole('link', { name: 'View all history' })).toBeInTheDocument();
	});
});
