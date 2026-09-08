import { render } from '@testing-library/svelte';
import { axe, toHaveNoViolations } from 'jest-axe';
import { createRawSnippet } from 'svelte';
import { describe, expect, it, vi } from 'vitest';

import type { CertificateRecord, DeniedRequest, PageMeta, ServiceEnrollment } from '$lib/api/types';

import AccountHoldersPanel from './AccountHoldersPanel.svelte';
import Alert from './Alert.svelte';
import AppRail from './AppRail.svelte';
import AuditTimeline from './AuditTimeline.svelte';
import BrandMark from './BrandMark.svelte';
import Button from './Button.svelte';
import CertRow from './CertRow.svelte';
import ConfirmModal from './ConfirmModal.svelte';
import CopyableId from './CopyableId.svelte';
import DeniedRow from './DeniedRow.svelte';
import DetailRow from './DetailRow.svelte';
import EmptyState from './EmptyState.svelte';
import ExpireCodeAction from './ExpireCodeAction.svelte';
import FilterChip from './FilterChip.svelte';
import FilterGroup from './FilterGroup.svelte';
import Footer from './Footer.svelte';
import Icon from './Icon.svelte';
import ListStatus from './ListStatus.svelte';
import LoadingBlock from './LoadingBlock.svelte';
import MonoChip from './MonoChip.svelte';
import OptionDiffList from './OptionDiffList.svelte';
import PageHeading from './PageHeading.svelte';
import PageSection from './PageSection.svelte';
import PageShell from './PageShell.svelte';
import Pager from './Pager.svelte';
import RailGroup from './RailGroup.svelte';
import RailItem from './RailItem.svelte';
import RailUserMenu from './RailUserMenu.svelte';
import RedemptionHistory from './RedemptionHistory.svelte';
import SearchInput from './SearchInput.svelte';
import SectionLabel from './SectionLabel.svelte';
import ServiceAccountRow from './ServiceAccountRow.svelte';
import ServiceCodeDetail from './ServiceCodeDetail.svelte';
import ServiceCodeFacts from './ServiceCodeFacts.svelte';
import ServiceCodeRow from './ServiceCodeRow.svelte';
import StatusBadge from './StatusBadge.svelte';
import ThemeToggle from './ThemeToggle.svelte';
import TypeBadge from './TypeBadge.svelte';
import TypeChip from './TypeChip.svelte';

expect.extend(toHaveNoViolations);

// One axe pass over every component the app renders.
//
// This is the file the CI job named "a11y" was believed to be running. It was
// not: the job ran the whole vitest suite, of which exactly one file —
// ConsentModal.a11y.test.ts — asserted anything about accessibility, so
// thirty-odd components had never been checked by anything at all. The audit
// that produced this file found a Level A defect in TypeBadge that a single
// axe() call would have caught on the commit that introduced it.
//
// Adding a component here is not optional. A component with no case is a
// component nothing checks, so the roster test at the bottom fails the build
// when one appears in this directory without an entry above it.

const now = new Date('2026-08-22T12:00:00Z');

/** text is a `children` snippet holding one string, for the wrappers that take one. */
function text(content: string) {
	return createRawSnippet(() => ({ render: () => `<span>${content}</span>` }));
}

function meta(overrides: Partial<PageMeta> = {}): PageMeta {
	return { total: 120, limit: 25, offset: 50, page: 3, page_count: 9, ...overrides };
}

function cert(overrides: Partial<CertificateRecord> = {}): CertificateRecord {
	return {
		id: 'cert-1',
		type: 'user',
		serial_number: '1',
		key_id: 'key-1',
		principals: 'alice',
		public_key_fingerprint: 'SHA256:abc',
		issued_at: '2026-08-22T10:00:00Z',
		expires_at: '2026-08-22T18:00:00Z',
		decided_by_username: 'alice',
		decided_by_email: 'alice@example.com',
		reported_username: 'alice',
		reported_hostname: 'alice-laptop',
		...overrides
	};
}

function denial(overrides: Partial<DeniedRequest> = {}): DeniedRequest {
	return {
		id: 'dec-1',
		certificate_request_id: 'req-1',
		type: 'pam',
		decided_at: '2026-08-02T10:00:00Z',
		reported_username: 'deploy',
		reported_hostname: 'rack07',
		pam_service: 'sudo',
		tty: 'pts/3',
		remote_host: '10.1.2.9',
		...overrides
	};
}

function enrollment(overrides: Partial<ServiceEnrollment> = {}): ServiceEnrollment {
	return {
		id: 'enr-1',
		service_account: 'svc-deploy',
		approved_by_username: 'alice',
		certificate_request_id: 'req-1',
		principals: ['svc-deploy'],
		key_id: 'svc-deploy/req-1',
		public_key_fingerprint: 'SHA256:abc',
		options: { extensions: [], no_touch_required: false },
		certificate_valid_seconds: 3600,
		created_at: '2026-08-20T12:00:00Z',
		expires_at: '2026-11-20T12:00:00Z',
		first_redeemed_at: '2026-08-22T10:00:00Z',
		last_retrieved_at: '2026-08-22T10:00:00Z',
		retrieval_count: 12,
		...overrides
	};
}

/**
 * The element to mount into, for the few components that are only valid
 * inside a particular parent.
 *
 * `DetailRow` is a `dt`/`dd` pair, which ARIA requires to be inside a `dl` —
 * all four call sites do exactly that, so rendering one bare here would fail
 * the sweep on the harness rather than on the component.
 */
const parents: Record<string, string> = { DetailRow: 'dl' };

/**
 * Every component, in the state a reader actually meets.
 *
 * Deliberately the interesting state rather than the empty one: a Pager on
 * page 3 of 9 renders the elision and the `aria-current` that a one-page
 * Pager renders neither of, and a busy button carries a different set of
 * attributes from an idle one. Where a component has two shapes worth
 * separating — a rail item collapsed and expanded, an alert that warns and
 * one that errors — both are listed.
 *
 * `any` for the component slot: these are thirty-odd unrelated Svelte
 * components in one array, so their only common supertype is the one that
 * accepts all of them.
 */
const cases: [string, any, Record<string, unknown>][] = [
	['AccountHoldersPanel', AccountHoldersPanel, { enrollmentId: 'enr-1' }],
	['Alert (info)', Alert, { children: text('Loading request…') }],
	['Alert (warning)', Alert, { variant: 'warning', title: 'Certificate verification is off' }],
	['Alert (error)', Alert, { variant: 'error', title: 'That did not go through' }],
	['AppRail', AppRail, { onsignout: vi.fn() }],
	['AppRail (collapsed)', AppRail, { collapsed: true, onsignout: vi.fn() }],
	['AuditTimeline', AuditTimeline, { events: [] }],
	['BrandMark', BrandMark, { size: 22 }],
	['Button (primary)', Button, { children: text('Approve') }],
	['Button (ghost)', Button, { variant: 'ghost', children: text('Cancel') }],
	['Button (busy)', Button, { busy: true, children: text('Signing…') }],
	['CertRow', CertRow, { cert: cert(), now, href: '/certs/cert-1' }],
	[
		'ConfirmModal',
		ConfirmModal,
		{
			title: 'Retire this code?',
			confirmLabel: 'Retire',
			busyLabel: 'Retiring…',
			onconfirm: vi.fn(),
			oncancel: vi.fn(),
			children: text('The code stops working immediately.')
		}
	],
	['CopyableId', CopyableId, { value: 'cert-1', label: 'Request' }],
	['EmptyState', EmptyState, { icon: 'certificate-off', title: 'No certificates yet' }],
	[
		'EmptyState (with action)',
		EmptyState,
		{
			icon: 'filter-off',
			title: 'No certificates match',
			children: text('Nothing matches the filters above.'),
			action: text('Clear filters')
		}
	],
	['DeniedRow', DeniedRow, { denial: denial(), now }],
	['DetailRow', DetailRow, { label: 'Principals', children: text('alice') }],
	['ExpireCodeAction', ExpireCodeAction, { expire: vi.fn(), onexpired: vi.fn() }],
	['FilterChip', FilterChip, { label: 'User', icon: 'id-badge', selected: true, onclick: vi.fn() }],
	[
		'FilterGroup',
		FilterGroup,
		{
			label: 'Type',
			selected: '',
			onselect: vi.fn(),
			options: [
				{ value: '', label: 'All', icon: 'filter-off' },
				{ value: 'user', label: 'User', icon: 'id-badge' }
			]
		}
	],
	['Footer', Footer, {}],
	['Icon (decorative)', Icon, { name: 'certificate' }],
	['Icon (labelled)', Icon, { name: 'certificate', ariaLabel: 'Certificate' }],
	['ListStatus', ListStatus, { message: '12 certificates found.' }],
	// Only the labelled case. The bars are gated behind 300ms and are
	// `aria-hidden` when they do arrive, so an unlabelled LoadingBlock hands
	// axe an empty container and a passing case that checked nothing. The
	// label is the part that reaches a reader; LoadingBlock.test.ts covers
	// the gate and the aria-hidden.
	['LoadingBlock', LoadingBlock, { shape: 'lines', label: 'Loading your account…' }],
	['MonoChip', MonoChip, { children: text('10.1.2.9') }],
	['OptionDiffList', OptionDiffList, { entries: [], emptyLabel: 'Nothing was trimmed' }],
	['PageHeading', PageHeading, { title: 'Certificate history' }],
	[
		'PageHeading (with back chip)',
		PageHeading,
		{ title: 'Certificate', back: { label: 'All certificates', href: '/admin/certificates' } }
	],
	['PageSection', PageSection, { title: 'Email notifications', children: text('Seven kinds.') }],
	['PageShell', PageShell, { children: text('Page body') }],
	['Pager', Pager, { meta: meta(), onpage: vi.fn() }],
	['Pager (single page)', Pager, { meta: meta({ page_count: 1 }), onpage: vi.fn() }],
	[
		'RailGroup',
		RailGroup,
		{
			label: 'Admin',
			icon: 'shield-lock',
			open: true,
			ontoggle: vi.fn(),
			children: text('rows')
		}
	],
	['RailItem', RailItem, { href: '/dashboard', label: 'Dashboard', icon: 'layout-dashboard' }],
	[
		'RailItem (collapsed)',
		RailItem,
		{ href: '/dashboard', label: 'Dashboard', icon: 'layout-dashboard', collapsed: true }
	],
	['RailUserMenu', RailUserMenu, { identity: 'alice@example.com', onsignout: vi.fn() }],
	['RedemptionHistory', RedemptionHistory, { retrievals: [], total: 0 }],
	['SearchInput', SearchInput, { label: 'Search certificates', onsearch: vi.fn() }],
	['SectionLabel', SectionLabel, { children: text('Filter') }],
	['SectionLabel (labelling a field)', SectionLabel, { for: 'x', children: text('Filter') }],
	[
		'ServiceAccountRow',
		ServiceAccountRow,
		{ account: 'svc-deploy', liveCount: 2, expiredCount: 1, now, onclick: vi.fn() }
	],
	['ServiceCodeDetail', ServiceCodeDetail, { enrollment: enrollment(), now }],
	['ServiceCodeFacts', ServiceCodeFacts, { enrollment: enrollment(), approvedBy: 'alice', now }],
	[
		'ServiceCodeRow',
		ServiceCodeRow,
		{ enrollment: enrollment(), now, href: '/service-codes/enr-1' }
	],
	['StatusBadge (pending)', StatusBadge, { status: 'pending' }],
	['StatusBadge (denied)', StatusBadge, { status: 'denied' }],
	['ThemeToggle (icon)', ThemeToggle, {}],
	['ThemeToggle (rail)', ThemeToggle, { variant: 'rail' }],
	['TypeBadge', TypeBadge, { type: 'user' }],
	['TypeBadge (unknown)', TypeBadge, {}],
	['TypeChip', TypeChip, { type: 'service' }]
];

describe('component accessibility', () => {
	for (const [name, Component, props] of cases) {
		it(`should have no axe violations when rendering ${name}`, async () => {
			const { container } = render(Component, props);

			// Re-parent after rendering rather than passing a container to
			// render(): @testing-library/svelte mounts into a div of its own
			// whatever container it is handed, so the wrapper has to be put
			// on afterwards to be the thing axe walks.
			const parent = parents[name.split(' ')[0]];
			let target: HTMLElement = container;
			if (parent) {
				target = document.body.appendChild(document.createElement(parent));
				target.append(...container.childNodes);
			}

			const results = await axe(target);
			expect(results).toHaveNoViolations();
		});
	}
});

describe('component accessibility roster', () => {
	it('should cover every component in this directory', () => {
		// import.meta.glob is resolved by Vite at build time, so this is the
		// real directory listing rather than a copy somebody has to remember
		// to update.
		const found = Object.keys(import.meta.glob('./*.svelte'))
			.map((path) => path.replace('./', '').replace('.svelte', ''))
			.sort();

		// Cases are named "Component" or "Component (state)", so the first
		// word of each names the component it covers.
		const covered = new Set(cases.map(([name]) => name.split(' ')[0]));

		// Covered elsewhere, each for a reason the sweep cannot serve:
		//
		// ConsentModal and its own a11y file — a native modal needs assertions
		// about focus and about Escape being blocked, not one render.
		//
		// ApprovalView, AdminServiceCodeDetail — both fetch on mount and take
		// a detail object big enough that a fixture here would be a second,
		// drifting copy of the one in their own test files. Their own suites
		// render them; see ApprovalView.test.ts and AdminServiceCodeDetail.test.ts.
		covered.add('ConsentModal');
		covered.add('ApprovalView');
		covered.add('AdminServiceCodeDetail');

		const uncovered = found.filter((component) => !covered.has(component));
		expect(uncovered).toEqual([]);
	});
});
