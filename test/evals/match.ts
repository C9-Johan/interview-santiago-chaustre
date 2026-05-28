/**
 * Deterministic lexical matching for the eval heuristics.
 *
 * Why stemming and not embeddings: these matchers run *inside the test* to decide pass/fail. A scoring
 * heuristic must be reproducible, free, and offline — an embedding call is nondeterministic (remote),
 * costs tokens per assertion, needs a key in CI, and forces a similarity threshold that is itself
 * overfit-prone. Stemming gives morphological robustness (available↔availability, date↔dates,
 * fee↔fees, book↔booking) with none of that. Its one weakness — true synonyms/paraphrase — is mitigated
 * by letting each facet declare a small explicit term set (see playbook.replyMustMention).
 *
 * The stemmer is a light, hand-rolled suffix stripper (no dependency): good enough for facet matching
 * over a known vocabulary, NOT a linguistically complete Porter implementation.
 */

/** Strip the common inflectional suffixes our vocabulary actually uses. Lexical, not linguistic. */
export function stem(word: string): string {
  let w = word.toLowerCase().replace(/[^a-z0-9]/g, '');
  if (w.length <= 3) return w;
  if (/ing$/.test(w) && w.length > 5) w = w.slice(0, -3); // booking → book
  else if (/ed$/.test(w) && w.length > 4) w = w.slice(0, -2); // booked → book
  else if (/(ss|sh|ch|x|z)es$/.test(w)) w = w.slice(0, -2); // taxes → tax, dishes → dish
  else if (/s$/.test(w) && !/ss$/.test(w)) w = w.slice(0, -1); // dates → date, fees → fee
  return w;
}

/** Tokenize into a set of stems (alphanumeric runs only; punctuation/symbols dropped). */
export function stemSet(text: string): Set<string> {
  return new Set(
    text
      .toLowerCase()
      .split(/[^a-z0-9]+/)
      .filter(Boolean)
      .map(stem),
  );
}

/**
 * True if the text surfaces any of the given terms. A term made of word characters is matched as a
 * stem against the text's stems (so morphology doesn't matter); a term with a symbol or hyphen
 * (e.g. "$", "all-in") is matched as a raw substring, since those carry meaning the tokenizer drops.
 */
export function mentionsAny(text: string, terms: readonly string[]): boolean {
  const stems = stemSet(text);
  const lower = text.toLowerCase();
  return terms.some((term) =>
    /^[a-z0-9]+$/i.test(term) ? stems.has(stem(term)) : lower.includes(term.toLowerCase()),
  );
}
