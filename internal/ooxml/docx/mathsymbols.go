package docx

// mathSymbols maps the Unicode characters Word writes into equations to their
// LaTeX commands. Word stores rendered glyphs rather than markup, so without
// this table an equation would carry raw symbols that LaTeX cannot typeset.
//
// A trailing space keeps a command from running into the following token
// ("\pi r" rather than "\pir").
var mathSymbols = map[rune]string{
	// Lowercase Greek.
	'α': "\\alpha ", 'β': "\\beta ", 'γ': "\\gamma ", 'δ': "\\delta ",
	'ε': "\\epsilon ", 'ζ': "\\zeta ", 'η': "\\eta ", 'θ': "\\theta ",
	'ι': "\\iota ", 'κ': "\\kappa ", 'λ': "\\lambda ", 'μ': "\\mu ",
	'ν': "\\nu ", 'ξ': "\\xi ", 'π': "\\pi ", 'ρ': "\\rho ",
	'σ': "\\sigma ", 'τ': "\\tau ", 'υ': "\\upsilon ", 'φ': "\\phi ",
	'χ': "\\chi ", 'ψ': "\\psi ", 'ω': "\\omega ",
	'ϑ': "\\vartheta ", 'ϕ': "\\varphi ", 'ϱ': "\\varrho ", 'ς': "\\varsigma ",

	// Uppercase Greek.
	'Γ': "\\Gamma ", 'Δ': "\\Delta ", 'Θ': "\\Theta ", 'Λ': "\\Lambda ",
	'Ξ': "\\Xi ", 'Π': "\\Pi ", 'Σ': "\\Sigma ", 'Υ': "\\Upsilon ",
	'Φ': "\\Phi ", 'Ψ': "\\Psi ", 'Ω': "\\Omega ",

	// Binary operators and relations.
	'×': "\\times ", '÷': "\\div ", '±': "\\pm ", '∓': "\\mp ",
	'⋅': "\\cdot ", '∗': "\\ast ", '⊕': "\\oplus ", '⊗': "\\otimes ",
	'≠': "\\neq ", '≤': "\\leq ", '≥': "\\geq ", '≈': "\\approx ",
	'≡': "\\equiv ", '∼': "\\sim ", '≅': "\\cong ", '∝': "\\propto ",
	'≪': "\\ll ", '≫': "\\gg ",

	// Set and logic.
	'∈': "\\in ", '∉': "\\notin ", '⊂': "\\subset ", '⊃': "\\supset ",
	'⊆': "\\subseteq ", '⊇': "\\supseteq ", '∪': "\\cup ", '∩': "\\cap ",
	'∅': "\\emptyset ", '∀': "\\forall ", '∃': "\\exists ",
	'∧': "\\land ", '∨': "\\lor ", '¬': "\\neg ",

	// Arrows.
	'→': "\\rightarrow ", '←': "\\leftarrow ", '↔': "\\leftrightarrow ",
	'⇒': "\\Rightarrow ", '⇐': "\\Leftarrow ", '⇔': "\\Leftrightarrow ",
	'↦': "\\mapsto ",

	// Calculus and misc.
	'∞': "\\infty ", '∂': "\\partial ", '∇': "\\nabla ", '√': "\\sqrt ",
	'∫': "\\int ", '∑': "\\sum ", '∏': "\\prod ", '∮': "\\oint ",
	'°': "^{\\circ}", '′': "'", '″': "''",
	'ℓ': "\\ell ", 'ℏ': "\\hbar ", 'ℵ': "\\aleph ",
	'ℝ': "\\mathbb{R}", 'ℕ': "\\mathbb{N}", 'ℤ': "\\mathbb{Z}",
	'ℚ': "\\mathbb{Q}", 'ℂ': "\\mathbb{C}",
	'…': "\\ldots ", '⋯': "\\cdots ", '⋮': "\\vdots ", '⋱': "\\ddots ",
	'∠': "\\angle ", '∴': "\\therefore ", '∵': "\\because ",

	// Spaces Word emits inside equations.
	' ': " ", ' ': "\\,", ' ': "\\;",
}
