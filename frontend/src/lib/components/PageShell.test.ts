import { render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { describe, expect, it } from 'vitest';

import PageShell from './PageShell.svelte';

// Test methodology: render the shell around marker children and read back
// the width it applied. A width is a class name, which is not behaviour a
// test can assert on usefully — so the shell also stamps the name it was
// given onto the container, and that is what is pinned here: a page asking
// for "wide" gets the wide cap and not a silent fallback to the default.

const children = createRawSnippet(() => ({
	render: () => '<p data-testid="shell-child">page content</p>'
}));

const aside = createRawSnippet(() => ({
	render: () => '<p data-testid="shell-aside">filters</p>'
}));

describe('page shell', () => {
	it('should render the page inside it', () => {
		render(PageShell, { children, testid: 'shell' });

		expect(screen.getByTestId('shell-child')).toBeInTheDocument();
	});

	const widths = ['focus', 'default', 'wide', 'full'] as const;
	for (const width of widths) {
		it(`should apply the ${width} width when a page asks for it`, () => {
			render(PageShell, { children, width, testid: 'shell' });

			expect(screen.getByTestId('shell')).toHaveAttribute('data-page-width', width);
		});
	}

	// A page that says nothing about its width is a reading page, not a
	// table: defaulting to the widest would stretch prose across the glass.
	it('should default to the reading width when a page names none', () => {
		render(PageShell, { children, testid: 'shell' });

		expect(screen.getByTestId('shell')).toHaveAttribute('data-page-width', 'default');
	});

	it('should uncap the container at the full width', () => {
		render(PageShell, { children, width: 'full', testid: 'shell' });

		expect(screen.getByTestId('shell')).toHaveClass('max-w-none');
	});

	it('should cap the container at every other width', () => {
		render(PageShell, { children, width: 'wide', testid: 'shell' });

		expect(screen.getByTestId('shell')).toHaveClass('max-w-[1120px]');
	});

	it('should render a secondary column when one is given', () => {
		render(PageShell, { children, aside, testid: 'shell' });

		expect(screen.getByTestId('shell-aside')).toBeInTheDocument();
	});

	// The two-column arrangement only exists when there is something to put
	// in the second column; without it the main column must not be narrowed
	// to leave a gap for nothing.
	it('should not render a secondary column when none is given', () => {
		render(PageShell, { children, testid: 'shell' });

		expect(screen.queryByTestId('shell-aside')).not.toBeInTheDocument();
		expect(screen.getByTestId('shell').querySelector('aside')).toBeNull();
	});
});
