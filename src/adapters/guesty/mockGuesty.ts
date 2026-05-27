import type {
  AvailabilityResult,
  GuestyPort,
  ListingFacts,
  PostedNote,
} from '../../ports/GuestyPort.js';

/** A note as the mock stored it — exposed via getNotes() so /admin/notes can read the sink. */
export interface NoteRecord {
  id: string;
  conversationId: string;
  body: string;
  createdAt: string;
}

/** Flat cleaning fee folded into every availability total — deterministic for tests. */
const CLEANING_FEE = 75;

/**
 * The "Soho 2BR" fixture from CHALLENGE.md §6. Returned for any listingId: the webhook contract
 * often omits a resolvable listingId, and a single deterministic fixture keeps offline runs and
 * tests grounded in the same facts the example reply was written against.
 */
const SOHO_2BR: ListingFacts = {
  id: 'soho-2br',
  title: 'Soho 2BR',
  bedrooms: 2,
  amenities: ['self-check-in', 'wifi', 'full kitchen', 'courtyard bedroom'],
  houseRules: ['no parties', 'no smoking', 'quiet hours after 10pm'],
  basePrice: 200,
  address: 'Spring St, Soho',
};

/** Whole nights between two ISO dates; never negative, min 1 so a same-day range still books. */
function nightsBetween(from: string, to: string): number {
  const ms = new Date(to).getTime() - new Date(from).getTime();
  const nights = Math.round(ms / 86_400_000);
  return Math.max(1, nights);
}

export function createMockGuesty(): GuestyPort & { getNotes(): NoteRecord[] } {
  const notes: NoteRecord[] = [];
  let counter = 0;

  return {
    async getListing(_listingId: string): Promise<ListingFacts> {
      // listingId ignored on purpose — see SOHO_2BR comment.
      return SOHO_2BR;
    },

    async checkAvailability(
      _listingId: string,
      from: string,
      to: string,
    ): Promise<AvailabilityResult> {
      const nights = nightsBetween(from, to);
      return {
        available: true,
        nights,
        total: nights * SOHO_2BR.basePrice + CLEANING_FEE,
      };
    },

    async postNote(conversationId: string, body: string): Promise<PostedNote> {
      const id = `note_${++counter}`;
      notes.push({ id, conversationId, body, createdAt: new Date().toISOString() });
      return { id };
    },

    getNotes(): NoteRecord[] {
      return notes;
    },
  };
}
