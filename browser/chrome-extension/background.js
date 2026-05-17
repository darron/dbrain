"use strict";

const DEFAULT_BASE_URL = "http://127.0.0.1:8742";
const BADGE_RESET_MS = 2500;
const LOGIN_OPEN_THROTTLE_MS = 5000;
const ext = globalThis.browser || globalThis.chrome;
let lastLoginOpenAt = 0;

ext.runtime.onInstalled.addListener(async () => {
	const config = await getConfig();
	if (!config.baseUrl) {
		await setConfig({ baseUrl: DEFAULT_BASE_URL });
		await ext.runtime.openOptionsPage();
	}
});

ext.action.onClicked.addListener((tab) => {
	void saveCurrentTab(tab);
});

async function saveCurrentTab(tab) {
	const tabID = tab && typeof tab.id === "number" ? tab.id : undefined;
	try {
		const pageURL = normalizePageURL(tab && tab.url);
		const config = await getConfig();
		const baseUrl = normalizeBaseURL(config.baseUrl || DEFAULT_BASE_URL);
		const origin = originPattern(baseUrl);
		const hasPermission = await containsOriginPermission(origin);
		if (!hasPermission) {
			await showStatus(tabID, "PERM", "#a15c00", "Open dbrain Link Saver options and approve host access.");
			await ext.runtime.openOptionsPage();
			return;
		}

		const response = await postLink(baseUrl, pageURL);
		const detail = successDetail(response);
		await showStatus(tabID, "OK", "#1f7a3d", detail);
	} catch (err) {
		console.error("dbrain link save failed", err);
		const title = err instanceof AuthRequiredError
			? err.message
			: "Save failed. Check the extension service worker console.";
		await showStatus(tabID, "ERR", "#b3261e", title);
		if (err instanceof AuthRequiredError) {
			await openLoginTab(err.loginURL);
		}
	}
}

async function postLink(baseUrl, pageURL) {
	const endpoint = new URL("/api/links", baseUrl);
	const response = await fetch(endpoint.toString(), {
		method: "POST",
		headers: {
			Accept: "application/json",
			"Content-Type": "application/json",
		},
		credentials: "include",
		body: JSON.stringify({ url: pageURL, enrich: false }),
	});
	const bodyText = await response.text();
	const body = parseJSONBody(bodyText);
	if (response.status === 401) {
		const loginURL = new URL("/login", baseUrl);
		loginURL.searchParams.set("return_to", "/");
		throw new AuthRequiredError("Log in to dbrain and click again.", loginURL.toString());
	}
	if (!response.ok) {
		throw new Error(errorDetail(response, body, bodyText));
	}
	return body;
}

function normalizePageURL(raw) {
	const value = String(raw || "").trim();
	if (!value) {
		throw new Error("No active tab URL.");
	}
	const parsed = new URL(value);
	if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
		throw new Error("Only http and https pages can be saved.");
	}
	return parsed.toString();
}

function normalizeBaseURL(raw) {
	const value = String(raw || "").trim();
	if (!value) {
		throw new Error("Set the dbrain base URL in extension options.");
	}
	const parsed = new URL(value);
	if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
		throw new Error("The dbrain base URL must start with http:// or https://.");
	}
	parsed.pathname = "/";
	parsed.search = "";
	parsed.hash = "";
	return parsed.toString();
}

function originPattern(baseUrl) {
	const parsed = new URL(baseUrl);
	return `${parsed.protocol}//${parsed.host}/*`;
}

function successDetail(body) {
	const queued = body && typeof body.queued === "number" ? body.queued : undefined;
	if (queued === 1) {
		return "Saved one link to dbrain.";
	}
	if (typeof queued === "number") {
		return `Saved link request to dbrain; queued=${queued}.`;
	}
	if (body && body.links && typeof body.links.queued === "number") {
		return `Saved link request to dbrain; queued=${body.links.queued}.`;
	}
	return "Saved link to dbrain.";
}

function errorDetail(response, body, bodyText) {
	if (body && typeof body.error === "string" && body.error.trim()) {
		return body.error.trim();
	}
	if (body && typeof body.message === "string" && body.message.trim()) {
		return body.message.trim();
	}
	if (bodyText.trim()) {
		return bodyText.trim().slice(0, 240);
	}
	return `dbrain returned HTTP ${response.status}.`;
}

function parseJSONBody(text) {
	if (!text.trim()) {
		return {};
	}
	try {
		return JSON.parse(text);
	} catch {
		return {};
	}
}

async function showStatus(tabID, text, color, title) {
	const target = typeof tabID === "number" ? { tabId: tabID } : {};
	await ext.action.setBadgeText({ ...target, text });
	await ext.action.setBadgeBackgroundColor({ ...target, color });
	if (title) {
		await ext.action.setTitle({ ...target, title: `dbrain Link Saver: ${title}` });
	}
	setTimeout(() => {
		void ext.action.setBadgeText({ ...target, text: "" });
		void ext.action.setTitle({ ...target, title: "Save current page to dbrain" });
	}, BADGE_RESET_MS);
}

async function openLoginTab(loginURL) {
	const now = Date.now();
	if (now - lastLoginOpenAt < LOGIN_OPEN_THROTTLE_MS) {
		return;
	}
	lastLoginOpenAt = now;
	await ext.tabs.create({ url: loginURL });
}

async function getConfig() {
	return ext.storage.sync.get({ baseUrl: "" });
}

async function setConfig(config) {
	await ext.storage.sync.set(config);
}

async function containsOriginPermission(origin) {
	if (!ext.permissions || !ext.permissions.contains) {
		return true;
	}
	return ext.permissions.contains({ origins: [origin] });
}

class AuthRequiredError extends Error {
	constructor(message, loginURL) {
		super(message);
		this.name = "AuthRequiredError";
		this.loginURL = loginURL;
	}
}
