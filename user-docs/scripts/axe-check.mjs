// An axe-core pass over every page `astro build` produced.
//
// The frontend has had a component-level accessibility sweep for a while; the
// documentation site had nothing, and it is where most of the product's
// prose, every diagram and the whole generated API reference live. This is
// the gate for that half.
//
// Static HTML in jsdom, not a browser. That buys the structural rules —
// names, roles, landmarks, heading order, table semantics — which is where a
// generated static site actually goes wrong. It cannot decide anything that
// needs layout, and the two rules below say so rather than pretending.
//
//   node scripts/axe-check.mjs [dist-dir]
//
// Exits non-zero on any violation, printing the rule, the pages it appears
// on, and one example node per rule.

import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import process from 'node:process';

import { JSDOM } from 'jsdom';

const AXE = path.join(
	path.dirname(new URL(import.meta.resolve('axe-core')).pathname),
	'axe.min.js'
);

const dist = path.resolve(process.argv[2] ?? 'dist');

/**
 * Rules this scan cannot decide, with the reason each one is off.
 *
 * Not a place to put findings that are inconvenient. A rule belongs here only
 * when running it in jsdom produces an answer that is wrong rather than
 * merely incomplete.
 */
const OFF = {
	// Needs a layout engine and a cascade. jsdom has neither, so every element
	// resolves to the same computed colour and the rule reports nothing useful
	// either way. Contrast is held to instead by the token values themselves —
	// see the palette in frontend/src/app.css, whose control and text pairs are
	// measured rather than eyeballed.
	'color-contrast': { enabled: false },

	// Starlight renders the table of contents twice, once for each breakpoint,
	// and gives both the same "On this page" name. Only ever one of them is
	// displayed: the mobile copy is inside `lg:sl-hidden` and the desktop copy
	// inside `sl-hidden lg:sl-block`, so at any viewport width one of the two
	// is `display: none` and therefore out of the accessibility tree entirely.
	// A real screen reader meets one landmark. jsdom applies no CSS, sees both,
	// and reports a duplicate that does not exist.
	'landmark-unique': { enabled: false }
};

/**
 * Rules switched off for one part of the site, and only that part.
 *
 * Scoped rather than global because the whole value of a rule is that it
 * fires on the pages we write. `heading-order` is the example: switching it
 * off everywhere would have cost nothing visible and would also have hidden
 * the two FAQ pages that opened every question at `h3` under an `h1`.
 */
const SCOPED_OFF = [
	{
		// Every page under reference/api is rendered by starlight-openapi from
		// docs/openapi.yaml — no file in this repository decides their heading
		// levels. The plugin puts an `h5` "Example" heading inside an `h3`
		// section, skipping `h4`. Upstream; revisit on a plugin bump.
		prefix: 'reference/api/',
		rules: ['heading-order']
	}
];

/** pages lists every HTML file the build wrote. */
function pages(dir, found = []) {
	for (const entry of readdirSync(dir)) {
		const full = path.join(dir, entry);
		if (statSync(full).isDirectory()) {
			pages(full, found);
		} else if (entry.endsWith('.html')) {
			found.push(full);
		}
	}
	return found;
}

const axeSource = readFileSync(AXE, 'utf8');
const files = pages(dist).sort();

if (files.length === 0) {
	console.error(`No HTML under ${dist}. Run the build first.`);
	process.exit(2);
}

/** One entry per rule, accumulating the pages it fired on. */
const found = new Map();

for (const file of files) {
	const rel = path.relative(dist, file);
	const rules = { ...OFF };
	for (const scope of SCOPED_OFF) {
		if (rel.startsWith(scope.prefix)) {
			for (const rule of scope.rules) {
				rules[rule] = { enabled: false };
			}
		}
	}

	const dom = new JSDOM(readFileSync(file, 'utf8'), {
		runScripts: 'outside-only',
		pretendToBeVisual: true
	});
	try {
		dom.window.eval(axeSource);
		const results = await dom.window.axe.run(dom.window.document, {
			resultTypes: ['violations'],
			rules
		});
		for (const violation of results.violations) {
			if (!found.has(violation.id)) {
				found.set(violation.id, { impact: violation.impact, help: violation.help, pages: [], example: null });
			}
			const entry = found.get(violation.id);
			entry.pages.push(rel);
			entry.example ??= violation.nodes[0]?.html.replace(/\s+/g, ' ').slice(0, 220);
		}
	} catch (cause) {
		console.error(`could not scan ${rel}: ${cause.message}`);
		process.exitCode = 2;
	} finally {
		dom.window.close();
	}
}

console.log(`axe-core: scanned ${files.length} pages under ${path.relative(process.cwd(), dist)}`);

if (found.size === 0) {
	console.log('No violations.');
	process.exit(process.exitCode ?? 0);
}

const order = { critical: 0, serious: 1, moderate: 2, minor: 3 };
for (const [id, v] of [...found].sort((a, b) => (order[a[1].impact] ?? 9) - (order[b[1].impact] ?? 9))) {
	console.log(`\n[${(v.impact ?? 'unknown').toUpperCase()}] ${id} — ${v.help}`);
	console.log(`  ${v.pages.length} of ${files.length} pages, first: ${v.pages[0]}`);
	console.log(`  ${v.example}`);
}
console.log(`\n${found.size} rule(s) violated. See https://dequeuniversity.com/rules/axe/ for each.`);
process.exit(1);
