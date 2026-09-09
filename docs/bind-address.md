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

What it does give is every account on this Mac. A request arriving over loopback
is exempt from the API key — that is what lets the control panel and the menu
bar work, and it cannot tell them from a client another user account on this Mac
is running. So under every choice, including the two narrow ones, anyone who can
log in to this Mac can use the model API and open the control panel without the
key. Narrowing the bind keeps other machines out; it does not keep other
accounts on this one out.

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
offering it — for an IPv4 address, which is what Gropius enumerates. A bind
written as a host name or an IPv6 literal keeps its place in the list after it
goes away, because there is nothing to check it against.

## What Bonjour does

Gropius advertises itself over Bonjour under the wildcard choice, and not under
the private-network choice or a bind that narrowed to this Mac. The advert
travels over the local network, which is the network those exclude, so it would
name an address its recipients cannot reach. Clients on a private network are
pointed at an address or a name instead.

A bind to one specific address written into `host` does advertise. The advert
carries this Mac's name rather than the bound address, so a client that resolves
the name to one of this Mac's other addresses is refused; point such a client at
the bound address.

Advertising is decided at startup, like the bind. Changing the choice does not
stop an advert that is already running.

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

A key is required for what the server actually answers on. If the bind reaches
any machine other than this one, Gropius generates and saves a key rather than
serving a network open — and if it cannot save one, it drops that address and
serves this Mac instead.

So a choice that narrowed to this Mac needs no key: the private-network choice
on a Mac where it found no address to bind, or a specific address this Mac no
longer holds. Nobody off this Mac can reach either, and the panel says which
happened.

The key is settled after the addresses are taken and before anything is served,
so there is no moment at which a network address answers without it.

## Where to go next

- [Serve models over a mesh VPN](mesh-vpn.md) — the walk-through for the
  private-network choice.
- [Getting started](getting-started.md) — first run, first model, first request.
