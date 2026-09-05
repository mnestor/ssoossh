/**
 * Decoding for RequestDetailResponse.decided_policy_explanation and
 * CertificateResponse.decided_policy_explanation: the lifetime policy
 * engine's own record of how it arrived at a certificate's duration (see
 * server/service/lifetimepolicy.go's PolicyExplanation). The field is an
 * opaque JSON document on the wire — structure over a flat reason string,
 * so the shape can grow an axis without a migration — and this module
 * types and parses only the parts the UI renders today.
 */

/** The winning tier and the condition that matched it. */
export interface TierExplanation {
	name: string;
	condition: string;
}

/** The source rule that narrowed the result, if one matched. */
export interface SourceRuleExplanation {
	cidr: string;
}

/**
 * PolicyExplanation is the subset of the wire document this page shows: the
 * winning tier (when one matched), the ceilings, and the source rule that
 * narrowed them. The document carries more (enrollment and extensions
 * axes) that no page renders yet, so it is left untyped here rather than
 * guessed at.
 */
export interface PolicyExplanation {
	ceiling: string;
	effective_duration: string;
	tier?: TierExplanation;
	source_rule?: SourceRuleExplanation;
}

/**
 * parsePolicyExplanation defensively decodes the JSON document. Returns
 * null for anything that fails to parse or does not look like the shape
 * this page expects — the field is opaque JSON on the wire, and its
 * absence, corruption, or a future incompatible version are display gaps,
 * not fatal errors.
 */
export function parsePolicyExplanation(raw: string | undefined): PolicyExplanation | null {
	if (!raw) {
		return null;
	}

	let parsed: unknown;
	try {
		parsed = JSON.parse(raw);
	} catch {
		return null;
	}

	if (!parsed || typeof parsed !== 'object') {
		return null;
	}
	const doc = parsed as Record<string, unknown>;
	if (typeof doc.ceiling !== 'string' || typeof doc.effective_duration !== 'string') {
		return null;
	}

	const explanation: PolicyExplanation = {
		ceiling: doc.ceiling,
		effective_duration: doc.effective_duration
	};

	if (doc.tier && typeof doc.tier === 'object') {
		const tier = doc.tier as Record<string, unknown>;
		if (typeof tier.name === 'string' && typeof tier.condition === 'string') {
			explanation.tier = { name: tier.name, condition: tier.condition };
		}
	}

	if (doc.source_rule && typeof doc.source_rule === 'object') {
		const sourceRule = doc.source_rule as Record<string, unknown>;
		if (typeof sourceRule.cidr === 'string') {
			explanation.source_rule = { cidr: sourceRule.cidr };
		}
	}

	return explanation;
}
