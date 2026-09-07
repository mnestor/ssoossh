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

	const widths = ['focus', 'wide', 'full'] as const;
	for (const width of widths) {
		it(`should apply the ${width} width when a page asks for it`, () => {
			render(PageShell, { children, width, testid: 'shell' });

			expect(screen.getByTestId('shell')).toHaveAttribute('data-page-width', width);
		});
	}

	// A page that says nothing about its width is a page inside the app, and
	// every one of those is `wide`. The 760px reading width it used to fall
	// back to left /account and /preferences narrower than their neighbours,
	// so the column jumped on the way to them.
	it('should default to the width every page inside the app uses', () => {
		render(PageShell, { children, testid: 'shell' });

		expect(screen.getByTestId('shell')).toHaveAttribute('data-page-width', 'wide');
	});

	// The cap sits on the column inside the outer box, which is what
	// carries the optional vertical centring.
	it('should uncap the column at the full width', () => {
		render(PageShell, { children, width: 'full', testid: 'shell' });

		expect(screen.getByTestId('shell').firstElementChild).toHaveClass('max-w-none');
	});

	it('should cap the column at every other width', () => {
		render(PageShell, { children, width: 'wide', testid: 'shell' });

		expect(screen.getByTestId('shell').firstElementChild).toHaveClass('max-w-[1120px]');
	});

	// Sign-in is the one screen that centres in the remaining height; every
	// other page starts at the top so it does not move as content loads.
	it('should centre the page vertically when asked to', () => {
		render(PageShell, { children, center: true, testid: 'shell' });

		expect(screen.getByTestId('shell')).toHaveClass('justify-center');
	});

	it('should start the page at the top by default', () => {
		render(PageShell, { children, testid: 'shell' });

		expect(screen.getByTestId('shell')).not.toHaveClass('justify-center');
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
