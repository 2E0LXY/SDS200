// On-screen help: what each page and card is, why you would use it, and how.
//
// The text lives here rather than in index.html so the markup stays about
// controls and this stays about explanation. Nothing here talks to the
// scanner; it only decorates the page once, on load.
//
// Keys:
//   sections  - the section's aria-label
//   cards     - the card's <h2> text, or "<CAP>|<h2>" where two cards share a
//               heading (the cap is the DIRECT / FRONT PANEL / USB badge)
//   controls  - a CSS selector; the text becomes the control's tooltip

const HELP_SECTIONS = {
  'Dashboard':
    'A single screen of everything the scanner is doing right now, with a way into each area. Nothing here writes to the radio: it is all readback, so it is safe to leave open.',
  'Live Scanner':
    'What the radio is hearing this second — system, department, channel, frequency, talkgroup, unit ID and signal — plus the four hold scopes and avoid. Use this page while listening; use Control for keypad work.',
  'Front Panel Control':
    'Every key on the radio, over the network. The display panel below the sliders is the scanner’s real screen, read back after each key, so you can work the scanner’s own menus from here and see exactly what it shows.',
  'Scanner Memory':
    'What is programmed in the radio: searchable once indexed, and browsable level by level. Clicking a channel or talkgroup tunes the scanner to it and holds.',
  'Scan Setup':
    'What the scanner listens to: which favourites lists and quick keys are live, which service types are enabled, and where it thinks it is. Change these when you are hearing the wrong things, or nothing.',
  'Search Close Call and Weather':
    'Finding activity that is not programmed: band and custom searches, Close Call for strong nearby transmitters, weather monitoring and tone-out standby. Most of these are scanner-menu functions, opened from here.',
  'Analyze Discovery and Waterfall':
    'Measurement rather than listening: signal and decode quality, channel activity, LCN discovery, an RF power plot and the waterfall. These take the radio out of normal scanning while they run.',
  'Audio and Recording':
    'Hearing the scanner on this device, and keeping a copy. The scanner serves one audio stream at a time, so starting it here stops anyone else listening.',
  'Scanner Settings':
    'Display, reception helpers and general configuration. Anything the remote protocol cannot set safely is opened in the scanner’s own menu instead of being faked here.',
  'System and Diagnostics':
    'The plumbing: finding the radio, where it thinks it is, the assistant, the licence, who holds control, and the logs you would send if something misbehaves.',
};

const HELP_CARDS = {
  // ---- Dashboard tiles ------------------------------------------------
  'Live Scanner': {
    what: 'A summary of the current channel, signal and hold state.',
    why: 'Tells you at a glance whether the radio is scanning, holding or silent.',
    how: 'Press OPEN for the full Live Scanner page.',
  },
  'Controls': {
    what: 'Volume, squelch, the rotary and the keypad.',
    why: 'Everything you would reach for on the front panel.',
    how: 'Press OPEN for the Front Panel page.',
  },
  'Lists & Quick Keys': {
    what: 'Which favourites lists and quick keys are enabled.',
    why: 'The usual reason for hearing nothing is a list or quick key switched off.',
    how: 'Press OPEN for Scan Setup.',
  },
  'RF / Waterfall': {
    what: 'Waterfall, system status, activity and LCN tools.',
    why: 'For looking at the band rather than listening to it.',
    how: 'Press OPEN, or one of the shortcut buttons for a specific view.',
  },
  'Audio & Recording': {
    what: 'Live audio to this device, and recording.',
    why: 'Listening and keeping a copy of what was heard.',
    how: 'Press OPEN for the Audio page.',
  },
  'System & Diagnostics': {
    what: 'Connection, location, licence, operator control and logs.',
    why: 'Where to go when something is not working.',
    how: 'Press OPEN for the System page.',
  },

  // ---- Front panel ----------------------------------------------------
  'Scanner Entry / Confirmation': {
    what: 'The scanner’s own screen, read back over the network after every key.',
    why: 'Confirms the radio actually did what the key asked, and lets you drive the scanner’s menus without guessing what is on screen.',
    how: 'Press Menu to open the scanner menu, then click any line to select it. Back steps up a level, Exit leaves the menu. Refresh re-reads the screen.',
  },

  // ---- Memory ---------------------------------------------------------
  'Search Programmed Memory': {
    what: 'A searchable index of every programmed channel and talkgroup, built by walking the radio’s memory.',
    why: 'The scanner itself cannot search: finding a name means stepping through favourites, systems, departments and channels. The index makes it instant, and survives a restart.',
    how: 'Press Build index once (under a minute for the favourites lists). Then type a name, place or service. Click a result to tune the radio to it. Names in the radio are mostly licence holders, so a ground is listed under the club’s company name. Include full database walks the in-range database entries too, and takes longer.',
  },
  'Scanner Memory Browser': {
    what: 'The memory tree exactly as the radio holds it: favourites, then systems, then departments or sites, then channels, talkgroups and site frequencies.',
    why: 'Useful when you want the structure rather than a name — to see what is in a system, or which department a channel sits in.',
    how: 'Click a row to go a level deeper; Back and Top move up. Clicking a channel or talkgroup tunes the scanner to it and holds.',
  },
  'Manage Favorites / Programming': {
    what: 'Creating and editing favourites, systems, sites, departments, channels, unit IDs, alerts and avoids.',
    why: 'This is programming, not control: a mistyped write here would corrupt the radio’s memory, so it is done in the scanner’s own menu where every entry is confirmed on screen.',
    how: 'Press Open Menu and work through the scanner menu on the Control page. The web memory browser stays read-only.',
  },
  'Bulk Programming & Database': {
    what: 'Profile management, database updates and wholesale favourites programming.',
    why: 'These move files on the microSD card, which the remote protocol cannot do at all.',
    how: 'Use Sentinel over USB, or the card in a reader. Nothing on this page pretends to do it over the network.',
  },

  // ---- Scan setup -----------------------------------------------------
  'Favorites Quick Keys': {
    what: 'The favourites quick keys the radio has, and whether each is enabled.',
    why: 'Quick keys are the fastest way to switch whole blocks of channels in and out of the scan.',
    how: 'Green is enabled, grey disabled, dim means no list is assigned. A normal click opens the matching memory. Editing changes what the scanner listens to, so it has to be switched on deliberately with Enable FQK editing.',
  },
  'Lists to Monitor': {
    what: 'Which favourites lists, database and search-with-scan sources the radio reports as live.',
    why: 'If a list is not being monitored, nothing in it will ever be heard, however it is programmed.',
    how: 'Press Refresh to re-read from the scanner.',
  },
  'FRONT PANEL|Service Types': {
    what: 'The scanner’s own Service Types selection screen.',
    why: 'Service types filter the database by what a channel is for. Turning off the ones you never want cuts a great deal of noise.',
    how: 'Press Open Service Types and choose on the scanner. The live grid lower down does the same thing directly.',
  },
  'DIRECT|Service Types': {
    what: 'Live readback of every service type, with a toggle for each.',
    why: 'Same effect as the scanner’s menu, without leaving the browser, and the radio is re-read after every change so the display cannot drift from reality.',
    how: 'Tap a type to toggle it. Only the types this radio actually has are listed, which is why a name you expect may not appear.',
  },
  'Location & Range': {
    what: 'The scanner’s own location and range screens.',
    why: 'The database is filtered by where the radio thinks it is and how far it should reach; a wrong location means wrong channels.',
    how: 'Use the postcode card on the System page for the easy route. These buttons open the radio’s own screens for anything they do not cover.',
  },
  'System / Department / Site Quick Keys': {
    what: 'Enabling and disabling quick keys below favourites level.',
    why: 'Lets you mute one system or department without touching the rest of a list.',
    how: 'Press Open Menu and use Manage Quick Key Status on the scanner.',
  },
  'Priority / Close Call / Weather Priority': {
    what: 'The operational priority toggles.',
    why: 'Priority interrupts what you are listening to when something more important appears.',
    how: 'Press the button for the function you want. Three-second long presses cannot be sent over the network, so anything needing one has to be done on the radio itself.',
  },

  // ---- Search ---------------------------------------------------------
  'Quick Search / ID Search': {
    what: 'Func + E/Yes, which is Quick Search from a channel hold and an ID Search toggle while scanning an eligible trunked system.',
    why: 'Quick Search sweeps from the frequency you are on without programming anything.',
    how: 'Hold a channel first, then press the button. What it does depends on what the radio is doing, so check the display panel afterwards.',
  },
  'Search Keys 1–3': {
    what: 'The three search keys, SRCH1 to SRCH3, as assigned on the radio.',
    why: 'One press runs a search you have set up, instead of walking the menu each time.',
    how: 'From a channel hold, press the key you want. What each one runs is set in the scanner’s menu.',
  },
  'Custom Search / Search with Scan': {
    what: 'The ten custom searches: limits, modulation, step, delay, attenuation and hold time.',
    why: 'A custom search sweeps a frequency range you choose — this is a frequency range, not the scan range in miles.',
    how: 'Press Open Menu and configure under Search For on the scanner.',
  },
  'Close Call': {
    what: 'Close Call, which catches strong transmitters near the radio whether or not they are programmed.',
    why: 'Finds the handheld in the same room or the lorry in the car park, which no amount of programming would predict.',
    how: 'Press Open Menu for Close Call Only, Hits with Scan, DND, alerts, pause and band selection.',
  },
  'Weather': {
    what: 'Weather scan, alerts and SAME programming, where the regional model supports them.',
    why: 'Mostly relevant to North American models; a UK SDS200E has little to show here.',
    how: 'Press Open Menu. If the options are missing, this radio does not have them.',
  },
  'Tone-Out': {
    what: 'Tone-out standby and setup, for two-tone paging.',
    why: 'Leaves the radio silent until a specific tone pair is sent.',
    how: 'Press Open Menu and use Tone-Out Standby or Tone-Out Setup on the scanner.',
  },

  // ---- Analyse --------------------------------------------------------
  'System Status': {
    what: 'Signal strength, decode quality and activity for one site.',
    why: 'Tells you whether a trunked site is actually receivable where you are, rather than merely programmed.',
    how: 'Put in the site index, press Start, and read the panel. Pause/Resume stops without losing the session.',
  },
  'Current Activity': {
    what: 'What is active on a site right now.',
    why: 'Shows whether a quiet system is genuinely quiet or simply not being decoded.',
    how: 'Put in the site index and press Start.',
  },
  'LCN Finder': {
    what: 'Logical channel number discovery for trunked systems that need LCNs.',
    why: 'Without the right LCNs the radio follows voice traffic to the wrong frequency.',
    how: 'Put in the site index and press Start. Only eligible systems will return anything.',
  },
  'RF Power Plot': {
    what: 'Measured RF power on one frequency over time.',
    why: 'Good for checking an antenna, a repeater’s duty cycle, or whether a frequency is in use at all.',
    how: 'Enter the frequency, pick modulation and a sampling rate, then Start. This takes the radio out of scanning.',
  },
  'Trunking Discovery': {
    what: 'The scanner’s trunking discovery sessions.',
    why: 'Logs talkgroups and channels it was not told about, so unknown traffic can be identified.',
    how: 'Press Open Menu to create, run and review sessions on the radio.',
  },
  'Conventional Discovery': {
    what: 'Discovery across a plain frequency range.',
    why: 'Finds conventional activity and can store what it hears.',
    how: 'Press Open Menu and set limits, modulation, step, delay, logging and auto-store on the radio.',
  },

  // ---- Audio ----------------------------------------------------------
  'Live SDS200 Audio': {
    what: 'The scanner’s own audio, streamed over RTSP and played here.',
    why: 'Lets you listen anywhere on the network instead of at the radio.',
    how: 'Press Listen Live. The radio serves one session at a time, so this takes the stream from any other listener, and port 554 is only contacted when you press it. Stop when you are done, so the session is released cleanly.',
  },
  'Scanner SD Recording': {
    what: 'The scanner’s built-in recorder, writing to its microSD card.',
    why: 'Records with no computer involved and keeps the scanner’s own metadata.',
    how: 'Start and stop here; the files stay on the card and are reviewed in the scanner’s Replay menu.',
  },
  'Remote WebApp Recording': {
    what: 'Recording the live stream on the machine running this app.',
    why: 'Gives you a file you can download and send on, without touching the card.',
    how: 'Press Record Audio; the recording appears under Open recording when stopped. It needs the live stream, so it takes the scanner’s single audio session.',
  },
  'Review Scanner Recordings / Replay Options': {
    what: 'Playing, renaming and deleting the scanner’s own recordings, and the replay duration.',
    why: 'These files live on the card, which the remote protocol cannot browse.',
    how: 'Press Open Menu and use Replay Options on the radio.',
  },

  // ---- Settings -------------------------------------------------------
  'Display & Backlight': {
    what: 'Backlight behaviour, dimmer, keypad light and the simple/detail display modes.',
    why: 'Mostly comfort, but the detail display also decides how much the readback here can tell you.',
    how: 'Use the buttons, or Open Menu for the rest. Colour and black-and-white mode needs a long press on the radio itself.',
  },
  'Reception Helpers': {
    what: 'Attenuation, IF exchange, repeater reverse and per-channel volume offset.',
    why: 'The first things to reach for when a signal is overloading, birdieing or too quiet relative to everything else.',
    how: 'These are context-sensitive: hold the channel first, then press. Attenuation left on by mistake is a common cause of a radio that has "gone deaf", so check it before suspecting the aerial.',
  },
  'General Settings': {
    what: 'Key beep, clock, band defaults, auto shutoff, ID formats, scan display mode and scanner information.',
    why: 'Set once and forgotten, but worth checking when behaviour seems odd.',
    how: 'Press Open Menu.',
  },
  'Location / GPS': {
    what: 'Manual location, saved locations, range, GPS format and the serial port.',
    why: 'The radio filters the database by location; with a GPS attached it does so continuously.',
    how: 'Press Open Menu for Set Your Location, or use the postcode card on the System page.',
  },
  'Avoids & Review': {
    what: 'Reviewing and clearing avoided channels, systems, searches and IDs.',
    why: 'An avoid set weeks ago is the other common reason a channel never appears.',
    how: 'Press Open Menu and review each avoid list on the radio.',
  },
  'Firmware / Main Database / Profiles': {
    what: 'Firmware updates, the master database and Sentinel profiles.',
    why: 'All three write to the microSD card and would brick a radio if half-applied over a network.',
    how: 'Use Sentinel over USB mass storage. This card exists to say so plainly rather than offer a button that cannot work.',
  },

  // ---- System ---------------------------------------------------------
  'Scanner Discovery': {
    what: 'Finding the SDS200 on the local network.',
    why: 'Everything else needs the radio’s address, and DHCP moves it.',
    how: 'Scan Network looks for it; or type the address and press Verify & Use IP. Only a device that identifies itself as an SDS200 or SDS200E is accepted, so a wrong address fails rather than half-working.',
  },
  'Scanner Location & Range': {
    what: 'Setting the radio’s latitude, longitude and range from a postcode.',
    why: 'The database is filtered by location and range; typing coordinates by hand is awkward and easy to get wrong.',
    how: 'Enter a postcode, optionally a house number for a more exact position, and a range in miles. Look up resolves it, Set on scanner writes it. Range is distance from the radio, not a frequency range.',
  },
  'Assistant': {
    what: 'Plain-English control: it reads the radio and operates the same commands this page does.',
    why: 'Quicker than hunting for a control, and it knows the radio’s own vocabulary — service types, hold scopes, quick keys.',
    how: 'Type what you want: "hold this channel", "squelch 3", "tune 446.00625", "what is it scanning?", "set range to 25 miles". It needs an API key, set under API keys. Everything it does goes through the same confirmed commands, so a failure is reported rather than assumed.',
  },
  'Scanner Clock': {
    what: 'The radio’s clock.',
    why: 'Recordings and logs are timestamped from it, so a wrong clock makes them hard to line up.',
    how: 'Read shows what the radio has; Sync to this computer sets it from this machine.',
  },
  'Licence': {
    what: 'The trial and your licence key.',
    why: 'Control of the scanner is free for 14 days from first run; after that it needs a key. Reading status, the memory browser and the logs keep working either way, so you can always see the radio and activate.',
    how: 'Paste the key from your purchase and press Activate. Buy a key opens the checkout and carries the installation id shown here, so a lost key can be re-sent to the same machine. The check runs on this computer, so it stops casual sharing rather than being copy protection.',
  },
  'Operator Control': {
    what: 'The operator lease: one client at a time may write to the radio.',
    why: 'Two clients sending keys at once would fight over the scanner. Reading is unrestricted; writing needs the lease.',
    how: 'Take control claims it, Release gives it up, and it lapses on its own if a client goes away. The control token is only needed if the server was started with one.',
  },
  'Function Directory': {
    what: 'Where every function in the owner’s manual lives in this app.',
    why: 'Makes plain which functions are real network controls, which are reached through the radio’s own menu, and which need USB — rather than leaving you to guess.',
    how: 'Read across: the area on the left, the page on the right.',
  },
  'Raw Scanner Status': {
    what: 'The unparsed STS reply from the radio.',
    why: 'The ground truth when a value on a nicer-looking page looks wrong.',
    how: 'Press Read STS.',
  },
  'Persistent Diagnostic Log': {
    what: 'The application log: UDP, control, STS, RTSP/RTP, waterfall, HTTP, lease and browser events.',
    why: 'This is what to send when something misbehaves; it records what was sent and what the radio replied.',
    how: 'Filter by level, component or text. Download Log or Copy Diagnostics gives you something to attach to a report.',
  },
};

const HELP_CONTROLS = {
  '[data-hold="system"]': 'System hold — stays on this system and keeps scanning its departments and channels. Use it to follow one agency without losing the rest of it.',
  '[data-hold="department"]': 'Department hold — stays within this department. Narrower than system hold, wider than channel hold.',
  '[data-hold="site"]': 'Site hold — stays on this trunked site instead of roaming to another. Use it when one site is receivable and another is not.',
  '[data-hold="channel"]': 'Channel hold — stops on this channel only. The radio will not move on, even in silence.',
  '#liveHold': 'Hold / resume. The scanner is asked, then re-read, so the button reflects what the radio actually did.',
  '#livePrevious': 'Previous channel in the current scope.',
  '#liveNext': 'Next channel in the current scope.',
  '[data-key="L"]': 'Avoid — drops the current channel out of the scan. It stays avoided until cleared, which is a common reason a channel never comes back; clear avoids under Settings.',
  '[data-key="E"]': 'Yes / Enter — confirms a menu entry or completes a number.',
  '[data-key="."]': 'No / decimal point — declines a prompt, and the point in a frequency.',
  '[data-key="M"]': 'Menu / Back — opens the scanner menu, or steps back out of one.',
  '[data-key="F"]': 'Func — the shift key: press it, then the second key. The app sends both together and checks the radio took it, because a popup on screen can swallow the pair.',
  '[data-key="Y"]': 'Replay — plays back the last transmissions the scanner buffered.',
  '[data-key="V"]': 'Volume / light — the volume push function. The long press that changes the backlight cannot be sent over the network.',
  '[data-key="Z"]': 'ZIP / location — opens the radio’s location entry.',
  '[data-func="Z"]': 'Service types — opens the scanner’s service type selection (Func + the service key).',
  '[data-range]': 'Range — how far from the radio’s location the database is used, in miles. Not a frequency range.',
  '[data-key="<"]': 'Rotary left — one step anticlockwise.',
  '[data-key=">"]': 'Rotary right — one step clockwise.',
  '[data-key="^"]': 'Rotary push — select, the same as pressing the knob in.',
  '#volume': 'Volume, 0 to 29, as the radio reports it.',
  '#squelch': 'Squelch, 0 to 19. Raise it to silence noise, lower it to hear weak signals.',
  '#memIndexBuild': 'Walks the favourites lists and builds the search index. Under a minute on a typical set.',
  '#memIndexFull': 'Also walks the in-range database entries. Much slower, and only worth it if a name is missing from the favourites.',
  '#licBuy': 'Opens the checkout, carrying this installation id so a lost key can be re-sent.',
  '#licActivate': 'Checks the key’s signature and stores it. No network needed.',
  '#acquireLease': 'Claims the operator lease so this client may write to the radio.',
  '#releaseLease': 'Gives the lease up for another client.',
  '#syncClock': 'Sets the radio’s clock from this computer.',
  '#addrLookup': 'Turns the postcode (and house number, if given) into coordinates, without writing anything to the radio.',
  '#addrSet': 'Writes the looked-up latitude, longitude and range to the radio.',
  '#scanNetwork': 'Looks for an SDS200 on this network.',
  '#useManualIp': 'Checks the address identifies itself as an SDS200 before using it.',
  '#audioConnect': 'Opens the scanner\u2019s single audio session and starts playing it here, packet by packet, so the delay stays around a tenth of a second.',
  '#audioStop': 'Stops playback and tears the RTSP session down, freeing the scanner\u2019s one audio stream for another listener.',
  '#audioVolume': 'Listening level on this device only. It does not change the scanner\u2019s own volume.',
  '#downloadLog': 'Saves the log file, for attaching to a problem report.',
  '#copyDiagnostics': 'Copies a summary of versions, connection and recent errors to the clipboard.',
};

// ------------------------------------------------------------------ render

function helpBlock(h) {
  const el = document.createElement('details');
  el.className = 'help';
  const rows = [['What', h.what], ['Why', h.why], ['How', h.how]]
    .filter(([, v]) => v)
    .map(([k, v]) => `<dt>${k}</dt><dd>${v}</dd>`)
    .join('');
  el.innerHTML = `<summary>Help</summary><dl>${rows}</dl>`;
  return el;
}

function cardHelpKey(card) {
  const h2 = card.querySelector('h2');
  if (!h2) return null;
  const name = h2.textContent.trim();
  const cap = card.querySelector('.cap');
  if (cap) {
    const composite = `${cap.textContent.trim()}|${name}`;
    if (HELP_CARDS[composite]) return composite;
  }
  return HELP_CARDS[name] ? name : null;
}

function renderHelp() {
  // One description under each page heading.
  for (const page of document.querySelectorAll('.page')) {
    const text = HELP_SECTIONS[page.getAttribute('aria-label') || ''];
    const title = page.querySelector('.section-title');
    if (!text || !title || title.nextElementSibling?.classList.contains('section-desc')) continue;
    const p = document.createElement('p');
    p.className = 'section-desc';
    p.textContent = text;
    title.after(p);
  }
  // One collapsible help block in each card that has text for it.
  let cards = 0;
  for (const card of document.querySelectorAll('.card, .dash-card')) {
    if (card.querySelector(':scope > .help')) continue;
    const key = cardHelpKey(card);
    if (!key) continue;
    card.append(helpBlock(HELP_CARDS[key]));
    cards++;
  }
  // Tooltips on individual controls.
  let controls = 0;
  for (const [sel, text] of Object.entries(HELP_CONTROLS)) {
    for (const el of document.querySelectorAll(sel)) {
      if (el.title) continue;
      el.title = text;
      controls++;
    }
  }
  return { cards, controls };
}

// A header button opens or closes every help block at once, for reading the
// page as documentation rather than hunting for one answer.
function installHelpToggle() {
  const header = document.querySelector('.app-header');
  if (!header || document.getElementById('helpToggle')) return;
  const b = document.createElement('button');
  b.id = 'helpToggle';
  b.className = 'help-toggle';
  b.type = 'button';
  b.textContent = 'Help';
  b.title = 'Show or hide the help on every card';
  b.setAttribute('aria-pressed', 'false');
  b.onclick = () => {
    const open = b.getAttribute('aria-pressed') !== 'true';
    document.querySelectorAll('details.help').forEach(d => { d.open = open; });
    b.setAttribute('aria-pressed', String(open));
    b.classList.toggle('on', open);
  };
  header.append(b);
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', () => { renderHelp(); installHelpToggle(); });
} else {
  renderHelp();
  installHelpToggle();
}
