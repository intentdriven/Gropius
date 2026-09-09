# Bind address reference

Which addresses Gropius answers on, and what each choice in **Settings → Bind
address** binds.

## The three choices

| Choice | Answers on | Reached from |
|---|---|---|
| `0.0.0.0 — reachable from your whole network` | every address this Mac holds, and this Mac | any machine that can route to one of them |
| `127.0.0.1 — this Mac only` | this Mac | this Mac, including its other user accounts |
| `A private network` | the one address this Mac holds on a private network, and this Mac | machines on that network, and this Mac |

Gropius takes its addresses once, at startup. A choice saved while the server
is running applies when it next starts, and until then the **Connect** tab
lists what the server is answering on.

## This Mac is always in the bind

Whatever else a choice binds, Gropius also answers on this Mac's loopback
address. Narrowing the bind never costs you the control panel, the menu-bar
app, or a client running in another user account on the same Mac — all three
reach the server over loopback.

No other machine gains anything by it. Loopback is this Mac and this Mac alone,
on any network, under any bind.

The control panel is loopback-only under every choice, so what a machine
elsewhere reaches is the model API and not the panel.

## The private-network choice

Gropius reads this Mac's own interfaces and selects the address that sits on a
private network. It never asks the VPN, and it names no product: it reads the
interface an address is configured on and the range the address falls in, which
cannot tell one product on that range from another.

The address it selected is named in **Settings**, beside the choice.

### When more than one address matches

Gropius does not choose. It names the addresses it found, serves this Mac, and
leaves it to you: an arbitrary pick between two networks could bind the server
to the one you did not mean.

Bind that address directly instead — write it into `config.json` as `host` and
leave `bind_mode` out.

### When none matches

Gropius serves this Mac and says why, in the control panel and in the log. It
never falls back to a wider bind: the choice can only narrow.

An address that goes away while the server runs is not re-bound and not
replaced. The server stops receiving on it, and the **Connect** tab stops
offering it.

## What Bonjour does

Gropius advertises itself over Bonjour under the wildcard choice, and not under
the other two. The advert travels over the local network, which is the network
the narrower choices exclude, so it would name an address its recipients cannot
reach. Clients on a private network are pointed at an address or a name instead.

## What `config.json` carries

```json
{
  "host": "0.0.0.0",
  "bind_mode": ""
}
```

`bind_mode` is empty for a bind that `host` decides, or `"private-network"` for
the private-network choice. The two are separate fields: choosing the mode
leaves `host` as you set it, so switching the mode off puts your bind back.

`host` takes any address this Mac can listen on, or a host name. An IPv6
literal is accepted bracketed or bare — `"[::1]"` and `"::1"` are the same bind.
A value that is neither an address nor a name is refused, and Gropius starts
serving this Mac with your other settings kept.

## What an API key is required for

Every choice but `127.0.0.1` is treated as exposed, and Gropius generates and
saves a key rather than serving a network open. That includes the
private-network choice on a Mac where it found no address to bind: the
requirement follows the choice you made, not what the interfaces happen to
hold, so that it can never be relaxed by a network going away.

## Where to go next

- [Serve models over a mesh VPN](mesh-vpn.md) — the walk-through for the
  private-network choice.
- [Getting started](getting-started.md) — first run, first model, first request.
