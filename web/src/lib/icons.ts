// Phosphor icons (MIT, @phosphor-icons/core), imported as SVG text.

import i_open from "@phosphor-icons/core/assets/regular/arrow-square-out.svg?raw";
import i_copy from "@phosphor-icons/core/assets/regular/copy.svg?raw";
import i_more from "@phosphor-icons/core/assets/regular/dots-three.svg?raw";
import i_plus from "@phosphor-icons/core/assets/regular/plus.svg?raw";
import i_close from "@phosphor-icons/core/assets/regular/x.svg?raw";
import i_search from "@phosphor-icons/core/assets/regular/magnifying-glass.svg?raw";
import i_keyboard from "@phosphor-icons/core/assets/regular/keyboard.svg?raw";
import i_events from "@phosphor-icons/core/assets/regular/pulse.svg?raw";
import i_settings from "@phosphor-icons/core/assets/regular/gear-six.svg?raw";
import i_disconnect from "@phosphor-icons/core/assets/regular/plugs.svg?raw";
import i_brand from "@phosphor-icons/core/assets/regular/plugs-connected.svg?raw";
import i_retry from "@phosphor-icons/core/assets/regular/arrow-clockwise.svg?raw";
import i_key from "@phosphor-icons/core/assets/regular/key.svg?raw";
import i_lock from "@phosphor-icons/core/assets/regular/lock-key.svg?raw";
import i_ok from "@phosphor-icons/core/assets/regular/check-circle.svg?raw";
import i_warn from "@phosphor-icons/core/assets/regular/warning-circle.svg?raw";
import i_error from "@phosphor-icons/core/assets/regular/x-circle.svg?raw";
import i_info from "@phosphor-icons/core/assets/regular/info.svg?raw";
import i_pin from "@phosphor-icons/core/assets/regular/push-pin.svg?raw";
import i_down from "@phosphor-icons/core/assets/regular/arrow-down.svg?raw";
import i_up from "@phosphor-icons/core/assets/regular/arrow-up.svg?raw";
import i_lan from "@phosphor-icons/core/assets/regular/globe-hemisphere-west.svg?raw";
import i_arrow from "@phosphor-icons/core/assets/regular/arrow-right.svg?raw";
import i_caret from "@phosphor-icons/core/assets/regular/caret-down.svg?raw";
import i_reverse from "@phosphor-icons/core/assets/regular/cloud-arrow-up.svg?raw";
import i_socks from "@phosphor-icons/core/assets/regular/shield-check.svg?raw";
import i_terminal from "@phosphor-icons/core/assets/regular/terminal-window.svg?raw";
import i_tag from "@phosphor-icons/core/assets/regular/tag.svg?raw";

export const icons = {
  open: i_open,
  copy: i_copy,
  more: i_more,
  plus: i_plus,
  close: i_close,
  search: i_search,
  keyboard: i_keyboard,
  events: i_events,
  settings: i_settings,
  disconnect: i_disconnect,
  brand: i_brand,
  retry: i_retry,
  key: i_key,
  lock: i_lock,
  ok: i_ok,
  warn: i_warn,
  error: i_error,
  info: i_info,
  pin: i_pin,
  down: i_down,
  up: i_up,
  lan: i_lan,
  arrow: i_arrow,
  caret: i_caret,
  reverse: i_reverse,
  socks: i_socks,
  terminal: i_terminal,
  tag: i_tag,
} as const;

export type IconName = keyof typeof icons;
