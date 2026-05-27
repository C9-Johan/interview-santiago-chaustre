/**
 * Port for the Guesty PMS integration. Adapters: `mockGuesty` (in-memory, default/offline) and
 * `httpGuesty` (real REST — TODO). The reply generator calls getListing/checkAvailability as LLM
 * tools; the pipeline calls postNote to deliver the result as an internal note.
 */

export interface ListingFacts {
  id: string;
  title: string;
  bedrooms: number;
  amenities: string[];
  houseRules: string[];
  basePrice: number;
  address?: string;
}

export interface AvailabilityResult {
  available: boolean;
  nights: number;
  /** All-in total in USD for the requested window. */
  total: number;
}

export interface PostedNote {
  id: string;
}

export interface GuestyPort {
  getListing(listingId: string): Promise<ListingFacts>;
  checkAvailability(listingId: string, from: string, to: string): Promise<AvailabilityResult>;
  /** Posts an internal note (type: "note") to the conversation — never reaches the guest. */
  postNote(conversationId: string, body: string): Promise<PostedNote>;
}
