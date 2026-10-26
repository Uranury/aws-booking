// Minimal single-page frontend. Routes live in the URL hash:
//   #/                 events list
//   #/event/<id>       seat grid and booking form
//   #/booking/<id>     booking status (polls until CONFIRMED or FAILED)
// The API is on the same origin under /api (CloudFront forwards it), so no CORS.

const app = document.getElementById("app");
const userId = getUserId();

window.addEventListener("hashchange", route);
route();

function route() {
  const [, page, id] = location.hash.split("/");
  if (page === "event" && id) return showEvent(decodeURIComponent(id));
  if (page === "booking" && id) return showBooking(decodeURIComponent(id));
  return showEvents();
}

async function showEvents() {
  const { ok, body } = await api("/events");
  if (!ok) return showError(body);
  render(
    el("h2", "Events"),
    el("ul", ...body.events.map((e) =>
      el("li", link(`#/event/${e.eventId}`, e.name), ` - ${e.venue}, ${formatDate(e.startsAt)}, ${formatPrice(e.priceCents)}`)
    ))
  );
}

async function showEvent(eventId) {
  const { ok, body: event } = await api(`/events/${encodeURIComponent(eventId)}`);
  if (!ok) return showError(event);

  let selected = null;
  const bookButton = el("button", "Book seat");
  bookButton.disabled = true;
  const forceFail = el("input");
  forceFail.type = "checkbox";
  const message = el("p");

  const seats = el("div", ...event.seats.map((seat) => {
    const b = el("button", String(seat.seatNumber));
    b.className = `seat-${seat.status}`;
    b.title = seat.status;
    b.disabled = seat.status !== "AVAILABLE";
    b.onclick = () => {
      seats.querySelectorAll(".selected").forEach((x) => x.classList.remove("selected"));
      b.classList.add("selected");
      selected = seat.seatNumber;
      bookButton.disabled = false;
    };
    return b;
  }));
  seats.className = "seats";

  bookButton.onclick = async () => {
    bookButton.disabled = true;
    message.textContent = "Booking…";
    const { ok, body } = await api("/bookings", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ userId, eventId, seatNumber: selected, forcePaymentFailure: forceFail.checked }),
    });
    if (!ok) {
      message.textContent = body.error || "Booking failed";
      bookButton.disabled = false;
      return;
    }
    location.hash = `#/booking/${body.bookingId}`;
  };

  render(
    el("h2", event.name),
    el("p", `${event.venue}, ${formatDate(event.startsAt)}, ${formatPrice(event.priceCents)} - ${event.available} of ${event.seatCount} seats available`),
    seats,
    el("p", el("label", forceFail, " Simulate payment failure (demo)")),
    bookButton,
    message
  );
}

async function showBooking(bookingId) {
  const status = el("p", "Status: PENDING");
  const details = el("p");
  render(el("h2", "Booking"), el("p", `Booking id: ${bookingId}`), status, details);

  // The booking is looked up through a secondary index, which can lag a moment
  // behind the write, so a 404 right after booking just means "not visible yet".
  for (let attempt = 0; attempt < 30; attempt++) {
    if (location.hash !== `#/booking/${bookingId}`) return; // user navigated away
    const { ok, status: code, body } = await api(`/bookings/${encodeURIComponent(bookingId)}`);
    if (ok) {
      status.textContent = `Status: ${body.status}`;
      if (body.status === "CONFIRMED") {
        details.replaceChildren(`Seat ${body.seatNumber} is yours. `, link(`#/event/${body.eventId}`, "Back to event"));
        return;
      }
      if (body.status === "FAILED") {
        details.replaceChildren(`Reason: ${body.failureReason}. `, link(`#/event/${body.eventId}`, "Back to event"));
        return;
      }
    } else if (code !== 404) {
      return showError(body);
    }
    await new Promise((r) => setTimeout(r, 1000));
  }
  details.textContent = "Still pending. Refresh the page to check again.";
}

// --- helpers ---------------------------------------------------------------

async function api(path, options) {
  try {
    const res = await fetch(`/api${path}`, options);
    const body = await res.json().catch(() => ({}));
    return { ok: res.ok, status: res.status, body };
  } catch {
    return { ok: false, status: 0, body: { error: "Network error" } };
  }
}

// el builds an element. Strings become text nodes, so API data is never parsed as HTML.
function el(tag, ...children) {
  const node = document.createElement(tag);
  node.append(...children);
  return node;
}

function link(href, text) {
  const a = el("a", text);
  a.href = href;
  return a;
}

function render(...nodes) {
  app.replaceChildren(...nodes);
}

function showError(body) {
  const p = el("p", body.error || "Something went wrong");
  p.className = "error";
  render(p, link("#/", "Back to events"));
}

function formatDate(iso) {
  return new Date(iso).toLocaleString();
}

function formatPrice(cents) {
  return cents === 0 ? "free" : (cents / 100).toFixed(2);
}

// A random id remembered in this browser. There is no login; this only groups
// one visitor's bookings under USER#<id>.
function getUserId() {
  try {
    let id = localStorage.getItem("userId");
    if (!id) {
      id = "user-" + crypto.randomUUID().slice(0, 8);
      localStorage.setItem("userId", id);
    }
    return id;
  } catch {
    return "user-guest";
  }
}
