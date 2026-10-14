// The theme state that draws a forward's or a session's state.

import { states, type StateName } from "./theme.gen";

export function themeState(state: string): StateName {
  switch (state) {
    case "active":
    case "connected":
      return "active";
    case "paused":
      return "paused";
    case "failed":
      return "failed";
    case "starting":
      return "starting";
    case "connecting":
    case "reconnecting":
      return "connecting";
  }
  return "closed";
}

export function glyph(state: string): string {
  return states[themeState(state)].glyph;
}
