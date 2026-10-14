import "./app.css";

import { mount } from "svelte";

import App from "./App.svelte";
import { app } from "./lib/store.svelte";

const target = document.getElementById("app");
if (target) {
  mount(App, { target });
  void app.start();
}
