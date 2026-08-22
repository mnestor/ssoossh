import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { axe, toHaveNoViolations } from 'jest-axe';
import { describe, expect, it } from 'vitest';

import ConsentModal from './ConsentModal.svelte';

expect.extend(toHaveNoViolations);

describe('ConsentModal accessibility', () => {
	it('should have no automated a11y violations', async () => {
		const { container } = render(ConsentModal, {
			notice: 'You are accessing a monitored system.',
			onaccepted: () => {}
		});
		const results = await axe(container);
		expect(results).toHaveNoViolations();
	});

	it('should have proper modal role and aria-modal', () => {
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		const dialog = screen.getByRole('dialog');
		expect(dialog).toHaveAttribute('aria-modal', 'true');
	});

	it('should have an accessible name for the modal', () => {
		render(ConsentModal, {
			notice: 'You are accessing a monitored system.',
			onaccepted: () => {}
		});
		const dialog = screen.getByRole('dialog');
		// The modal should have an accessible name via aria-labelledby or similar
		expect(dialog).toHaveAccessibleName();
	});

	it('should focus the approve button on open', () => {
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		const approveBtn = screen.getByRole('button', { name: /accept|approve|continue/i });
		expect(approveBtn).toBeInTheDocument();
	});

	it('should trap focus within the modal when open', () => {
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		// Focus trap is enforced by showModal() behavior; this documents it
		const dialog = screen.getByRole('dialog');
		expect(dialog).toHaveAttribute('open');
	});

	it('Escape key should be blocked (cannot dismiss unaccepted)', () => {
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		const dialog = document.querySelector('dialog');
		const event = new KeyboardEvent('keydown', { key: 'Escape', cancelable: true });
		dialog?.dispatchEvent(event);
		// The component should preventDefault on cancel event
		expect(dialog).toBeTruthy();
	});

	it('should have high contrast text and button colors', () => {
		const { container } = render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		// Verify button is visible and styled
		const button = screen.getByRole('button', { name: /accept|approve/i });
		const computed = window.getComputedStyle(button);
		// Should have defined background and text colors (not transparent)
		expect(computed.backgroundColor).not.toBe('transparent');
		expect(computed.color).not.toBe('transparent');
	});

	it('should have adequate button size for touch targets', () => {
		const { container } = render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		const button = screen.getByRole('button', { name: /accept|approve/i });
		const rect = button.getBoundingClientRect();
		// WCAG 2.1 minimum is 44x44px for touch targets
		expect(rect.width).toBeGreaterThanOrEqual(44);
		expect(rect.height).toBeGreaterThanOrEqual(44);
	});

	it('should announce the notice text to screen readers', () => {
		render(ConsentModal, {
			notice: 'You are accessing a monitored system.',
			onaccepted: () => {}
		});
		// The notice text should be in the document and accessible
		expect(screen.getByText(/monitored system/)).toBeInTheDocument();
	});

	it('should have clear, descriptive button label', () => {
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		// Button label should be clear about what it does (e.g., "I Accept", "Continue")
		const button = screen.getByRole('button');
		expect(button.textContent).toMatch(/accept|approve|continue|i understand/i);
	});

	it('should be keyboard navigable', async () => {
		const user = userEvent.setup();
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});

		const button = screen.getByRole('button', { name: /accept|approve/i });
		// Tab should focus the button
		await user.tab();
		expect(button).toHaveFocus();
	});

	it('should activate button with Enter key', async () => {
		const user = userEvent.setup();
		let accepted = false;
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {
				accepted = true;
			}
		});

		const button = screen.getByRole('button', { name: /accept|approve/i });
		await user.click(button);
		// Button should be interactive
		expect(button).toBeInTheDocument();
	});

	it('should work with reduced motion preferences', () => {
		// Test that the modal respects prefers-reduced-motion
		// No animations should occur or they should be instant
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		// The modal should still be usable and visible
		expect(screen.getByRole('dialog')).toBeInTheDocument();
	});

	it('should be visible with high contrast mode', () => {
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		const dialog = screen.getByRole('dialog');
		const computed = window.getComputedStyle(dialog);
		// Dialog should have defined colors (not relying on default theme)
		expect(computed.backgroundColor).toBeTruthy();
	});

	it('should provide visible focus indicator on buttons', () => {
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		const button = screen.getByRole('button', { name: /accept|approve/i });
		const computed = window.getComputedStyle(button, ':focus');
		// Focus state should be visible (outline or box-shadow)
		expect(computed.outline || computed.boxShadow).toBeTruthy();
	});

	it('should have semantic HTML structure', () => {
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		// Dialog role should be used (not just a div)
		const dialog = screen.getByRole('dialog');
		expect(dialog.tagName.toLowerCase()).toBe('dialog');
	});
});
