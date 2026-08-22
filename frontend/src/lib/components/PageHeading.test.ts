import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';

import PageHeading from './PageHeading.svelte';

describe('PageHeading', () => {
	it('should render the title as the page heading', () => {
		render(PageHeading, { eyebrow: 'Activity', title: 'Recent decisions' });
		expect(screen.getByRole('heading', { level: 1, name: 'Recent decisions' })).toBeInTheDocument();
	});

	it('should render the eyebrow above the title', () => {
		render(PageHeading, { eyebrow: 'Activity', title: 'Recent decisions' });
		expect(screen.getByText('Activity')).toBeInTheDocument();
	});
});
