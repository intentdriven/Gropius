# The posture page

The **Posture** tab in the control panel is one page that says what is on. It
states, in the present tense, who can reach this server and by which addresses,
what a request has to carry, what Gropius announces, what it writes to its log
and what it records. Nothing on it is a warning, and nothing on it changes a
setting: **Settings** is where a setting changes, and the warning for a server
that is reachable from the network with no API key stays at the top of the
panel whatever this page says.

Every line is read from the same state snapshot the rest of the panel is drawn
from. The page adds no observation of its own, which is what keeps it honest:
where the snapshot cannot carry a fact, the line says what Gropius cannot see
rather than implying that it is fine. What the page says about the running
bind is read from the bind the sockets were acquired under, and never from the
stored configuration, which a save changes before a restart applies it.

## What each line says, and what it is read from

| Line | What it states | Read from |
| --- | --- | --- |
| **Who can reach it** | What the running bind acquired. Under the wildcard, that the server answers on every address this Mac holds, followed by the ones Gropius can name, a name ending in `.local` being this Mac's name on the local network rather than an address. Under a bind to one address, that address and this Mac, followed by the addresses clients can use; where the bound address cannot be written as a URL, that one more address is answered on. Under a loopback bind, this Mac and no other address. Whether the bind narrowed to this Mac, in the resolver's words. | The bind's reach, wildcard flag and bound address; the endpoint list; the bind's refusal. |
| **The private network** | Present when an address is on a private network, or the private-network bind is chosen, or it is the bind in force. Which addresses are on one; that the mark is read from the interface and the address range and not from the network itself; that sharing the network with other people's machines, or a feature of the network publishing this port to the internet, changes who reaches the address without changing the address or the mark. Under the private-network choice, which address it selected, or that the choice is saved and not in force until the next start. | The endpoint list's marks; the bind's chosen mode, the mode in force, and the selection. |
| **What carries a request** | That every address is plain HTTP, with no TLS. | A fact about Gropius, not of this server. |
| **This control panel** | That the panel and its own API answer on this Mac alone on every bind, and that every account on this Mac can open it. | A fact about Gropius, not of this server. |
| **A request from another machine** | Whether a request arriving from another machine has to carry the API key, whether one is set, and, when the bind reaches no other machine, that nothing arrives from one. | Whether a key is set; the bind's reach. |
| **A request from this Mac** | That a request from this Mac to a loopback address is served without the key, including a request from another account on this Mac. | Whether a key is set. |
| **The local network** | Whether Gropius is announcing this server over Bonjour to every machine on the local network, as a service named after this Mac's name, and what the announcement carries: this Mac's addresses, the port the listeners took, how many models are ready, whether a key is required, and the fixed words saying it speaks the OpenAI API under `/v1`. When it is not announcing, which of three things kept it off when Gropius started. | The decision made at start from the `advertise` setting and the bind then in force; the mode in force; the bind's reach; the port the listeners took; this Mac's name. |
| **The request log** | That each request to the API is written to the server log as method, path, status and duration, with no client address, prompt, answer or key; the level the log is at, and that it is kept in the logs folder of this account's data folder in a file created for this account alone. | The `log_level` setting, which is applied live. |
| **Request statistics** | Whether request statistics are being recorded; what a record holds and what it never holds; that the same store also records when a model was loaded or evicted and the settings in force; for how many months and within how much room records are kept, and where; how far back the records on disk reach and how much room they take, or that the store could not be opened; and that every account on this Mac can read them. When recording is off, that no request is recorded. | The statistics switch, the two retention settings, and the store's status. |

## The two key lines

A key set is required from the network and not from this Mac. A connection
from this Mac to a loopback address is served without the key, and a second
account on the same Mac makes exactly such a connection. That is why the page
carries two lines rather than one: a single sentence saying "a key is required"
is true of the network and false of this Mac.

## What the page cannot see

- **Who reaches an address.** The bind is observable; reachability is not.
  Which machines can reach an address is decided by the network the address is
  on, and Gropius reads no network's settings.
- **Every address under the wildcard.** A wildcard bind answers on every
  address this Mac holds. The list names the IPv4 addresses Gropius found and
  this Mac's local-network name; an address it cannot name is answered on all
  the same, which is why the line says "every address this Mac holds" first.
- **What has happened to a private network.** The mark states which network an
  address belongs to. A sharing rule that invites other people's machines onto
  the network, or a feature of the network that publishes this port to the
  internet, changes who reaches the address and touches neither the interface
  nor the address, so the mark does not change. Whatever protection the network
  gives traffic between two machines is the network's doing, and Gropius neither
  performs nor observes it.
- **An announcement that failed to start.** The local-network line is read from
  the decision Gropius made when it started, from the setting and the bind
  then in force. A start that failed is reported in the log and not on this
  page.
- **A setting saved since the start.** The port, the bind and the `advertise`
  setting are taken once, when Gropius starts. A change saved since then is in
  the stored configuration and not in the sockets or the announcement, so the
  page reports what is running and the change takes effect at the next start.
- **The service name exactly as published.** The announcement's service name
  is built from this Mac's name and shortened when the name is too long for
  one; the page shows the name it was built from.
- **Where the log goes.** The request log is written to the server's own
  output; where the process running Gropius sends that output is the launcher's
  business.

## Where the announcement rule comes from

Gropius announces this server when all three hold: the `advertise` setting is
on, the bind in force is not the private-network choice, and the bind reaches
another machine. The page names the first of the three that does not hold.
Under the private-network choice the announcement is off because it travels
over the local network, which that choice excludes; a bind that narrowed to
this Mac reaches no other machine and so announces to none. A configuration
file Gropius could not read locks the bind to this Mac and switches announcing
off with it, and the page reports that state as it finds it.

The service is published as `Gropius (name)`, with this Mac's name filled in,
under a host name of Gropius's own rather than this Mac's `.local` name, which
macOS owns. A Mac whose name cannot be read is announced as `gropius`.

## Where to go next

- [Bind address reference](bind-address.md) — what each choice binds, and what
  happens when a choice cannot be honoured.
- [Serve models over a mesh VPN](mesh-vpn.md) — the how-to for the address the
  page marks, and the four things on the network's side to check.
- [What the statistics store records](statistics-store-reference.md) — the
  record, field by field.
