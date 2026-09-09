# Serve models over a mesh VPN

A mesh VPN gives each machine you sign in an address on a network of its own, so
the Mac running Gropius answers a laptop in another building without opening a
port on your router. Serving over one is the same job as
[serving over the local network](getting-started.md#5-talk-to-it--from-another-machine):
the address you hand out is different, and a few things on the VPN's side are
yours to check.

This page names Tailscale where an example helps; Gropius names no product, because it cannot tell one product on that address range from another. <!-- abcd-lint:allow: a person writing a page can say which product was reasoned about and tested against, which the classifier cannot -->

## Before you start

- Gropius is installed and running with at least one model downloaded — see
  [Getting started](getting-started.md).
- A mesh VPN is installed on this Mac and on the machine you want to reach it
  from, and both are signed in to the same network.

## Serve on the mesh address

1. Click the menu-bar icon and choose **Open Control Panel**.
2. Go to **Settings → Bind address** and choose
   **0.0.0.0 — reachable from your whole network**. Gropius binds either to this
   Mac alone or to every address it has, so the mesh address arrives with the
   local network attached.
3. Under **API key**, click **Generate a key**, then **Save settings**. Every
   request from a network has to carry it. (Gropius generates and saves a key
   itself if you leave a bind other than loopback without one, rather than
   serving open.)
4. Open the **Connect** tab. It lists the base URLs the server answers on. The
   address your mesh VPN gave this Mac carries a mark reading *private network*
   beside it. Copy that base URL.
5. On the other machine, point your client at the URL you copied:

   ```sh
   curl http://MESH-ADDRESS:11535/v1/chat/completions \
     -H "Authorization: Bearer YOUR_KEY" \
     -H "Content-Type: application/json" \
     -d '{"model":"mlx-community/Qwen3-0.6B-4bit","messages":[{"role":"user","content":"hi"}]}'
   ```

   If your mesh VPN gives its machines names, the name works in place of the
   address.

The control panel does not travel with the API: it and its `/api/*` endpoints
answer on loopback, so what a machine on the mesh reaches is the model API and
not the panel.

## What the mark beside an address means

Gropius marks an address that sits on a private network. The mark names the
network the address belongs to, and says nothing about how safe it is or who
else can reach it.

It is an observation about this Mac: Gropius reads the interface the address is
configured on and the range the address falls in. It cannot read your VPN's
settings, and the four things below all live there.

## Four things to check on the VPN's side

1. **Publishing to the internet.** Several mesh VPNs can also publish a machine's
   service to the public internet — Tailscale calls that feature Funnel. <!-- abcd-lint:allow: naming the feature is how a reader finds the switch to leave alone; the app names no product -->
   Switching it on for this port hands the server to the whole internet, and the
   mark does not budge: it describes the network the address is on, which has
   not changed.
2. **Sharing.** A sharing rule invites someone else's machines onto your network.
   It widens who reaches the server, and neither the address nor the mark
   changes when it does.
3. **Encryption.** The mark says nothing about encryption. Whatever a mesh VPN
   does to protect traffic between two machines, the VPN is doing it, and
   Gropius neither performs it nor observes it.
4. **Gropius speaks plain HTTP.** There is no TLS, on the local network or on
   the mesh address. So whatever protection the traffic has comes from the VPN
   and stops where the VPN stops — at a relay you route through, at a proxy in
   front of the server, or at the far end of a share.

None of the four is visible to Gropius, which is why the mark states which
network an address is on and leaves the rest to you.

## Where to go next

- [Getting started](getting-started.md) — the walk-through this page extends.
- [Record request statistics on this Mac](request-statistics.md) — what a served
  request leaves behind, and who on this Mac can read it.
