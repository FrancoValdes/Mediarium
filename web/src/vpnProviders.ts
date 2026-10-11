// What the VPN page tells people about each provider: where to get the
// WireGuard file, what to watch for, and the few values that are the same for
// every customer. A provider here is only a hint. Every one of them ends in the
// same fields (internal/vpn never treats a provider differently), and the list
// is deliberately plain: alphabetical, no ranking, no referral links.
//
// A fact goes in only when the provider's own help pages (or their own code
// repository) say it. Anything that could not be checked is left out, and the
// page asks the person to look at the fields after pasting. Sources for every
// line are in docs/vpn.md. No secrets live here, and no server addresses.
import { t } from './i18n.ts'
import type { WireGuardConf } from './wgConf.ts'

export interface VpnProvider {
  id: string
  name: string
  // Where the file comes from: the provider's own page, or its help guide.
  link: string
  linkIsGuide: boolean
  steps: string[]
  // Things that trip people up with this provider.
  notes: string[]
  // DNS servers that every customer of this provider gets. Filled in only when
  // the pasted file doesn't carry its own.
  dns: string[]
  // The provider's files carry a preshared key, and the tunnel never connects
  // without it.
  presharedKey: boolean
}

// "Not listed" keeps the plain paste flow. It is stored as 'custom', as before.
export const OTHER_PROVIDER = 'custom'

// Alphabetical by name. "Your own server" and "Not listed" come last.
export function vpnProviders(): VpnProvider[] {
  return [
    {
      id: 'airvpn',
      name: 'AirVPN',
      link: 'https://airvpn.org/generator/',
      linkIsGuide: false,
      steps: [
        t('Sign in to AirVPN and open Client Area, then Config Generator.'),
        t('Choose your device type, pick WireGuard, then pick a country or server.'),
        t('Generate the file and download it.'),
      ],
      notes: [t('AirVPN makes the keys for you. If you renew your keys later, your address changes and the old file stops working, so download a new file and add it again.')],
      dns: [],
      presharedKey: false,
    },
    {
      id: 'azirevpn',
      name: 'AzireVPN',
      link: 'https://manager.azirevpn.com/wireguard',
      linkIsGuide: false,
      steps: [
        t('Sign in to the AzireVPN manager and open the WireGuard configuration generator.'),
        t('Pick a location and let it make a new key pair.'),
        t('Generate the config file and save it.'),
      ],
      notes: [],
      dns: [],
      presharedKey: false,
    },
    {
      id: 'hideme',
      name: 'hide.me',
      link: 'https://hide.me/en/help/how-to-connect-via-wireguard-configuration-file/',
      linkIsGuide: true,
      steps: [
        t('Sign in to the members area and open WireGuard in the menu on the right.'),
        t('Press Generate keys, then pick a location.'),
        t('Press Download WireGuard config file.'),
      ],
      notes: [t('WireGuard files are for Premium accounts only. Each file is for one location, so download a new one to change location.')],
      dns: [],
      presharedKey: false,
    },
    {
      id: 'ipvanish',
      name: 'IPVanish',
      link: 'https://support.ipvanish.com/hc/en-us/articles/32069446435099-WireGuard-Linux-command-line-setup',
      linkIsGuide: true,
      steps: [
        t('Sign in at my.ipvanish.com and click WireGuard in the left menu.'),
        t('Pick a location and press Generate.'),
        t('Download the file.'),
      ],
      notes: [],
      dns: [],
      presharedKey: false,
    },
    {
      id: 'ivpn',
      name: 'IVPN',
      link: 'https://www.ivpn.net/account/wireguard-config',
      linkIsGuide: false,
      steps: [
        t('Sign in to the IVPN Client Area and open the WireGuard config generator.'),
        t('Press Generate key (or Add key if you made your own), then pick Single or Multi-hop, a server and a port.'),
        t('Download the zip archive and open the file for your server.'),
      ],
      notes: [],
      dns: [],
      presharedKey: false,
    },
    {
      id: 'mullvad',
      name: 'Mullvad',
      link: 'https://mullvad.net/en/account/wireguard-config',
      linkIsGuide: false,
      steps: [
        t('Log in with your account number.'),
        t('Under Platform choose Linux, then press Generate key.'),
        t('Pick a country, city or server, then press Download file.'),
      ],
      notes: [t('Use a separate key for each device. A Mullvad account can have up to 5 devices.')],
      dns: ['10.64.0.1'],
      presharedKey: false,
    },
    {
      id: 'nordvpn',
      name: 'NordVPN',
      link: 'https://support.nordvpn.com/hc/en-us/articles/19617516982929',
      linkIsGuide: true,
      steps: [
        t('Sign in to your Nord Account, open NordVPN, then Advanced Settings, then Set up NordVPN manually.'),
        t('Pick Server recommendation and a server, press Get setup configuration, choose WireGuard and press Download config.'),
      ],
      notes: [
        t("NordVPN's manual setup asks for your service credentials, which are not your Nord Account email and password. Their guide says where to find them."),
        t('The file needs a PrivateKey line. If yours has none, this app can\'t use it, and NordVPN\'s guides don\'t describe another way to get the key.'),
      ],
      dns: [],
      presharedKey: false,
    },
    {
      id: 'ovpn',
      name: 'OVPN',
      link: 'https://support.ovpn.com/hc/en-us/articles/46236349941523-How-do-I-enter-a-public-key-in-the-configuration-generator',
      linkIsGuide: true,
      steps: [
        t('Sign in to OVPN and open the WireGuard configuration generator.'),
        t('Let it make a key pair in your browser, or paste your own public key.'),
        t('Pick a server and download the file.'),
      ],
      notes: [t('If you gave OVPN your own public key, the file has a placeholder where the private key goes. Put your own private key there before you paste it.')],
      dns: [],
      presharedKey: false,
    },
    {
      id: 'pia',
      name: 'Private Internet Access',
      link: 'https://github.com/pia-foss/manual-connections',
      linkIsGuide: true,
      steps: [
        t('On a Linux computer with curl, jq and wireguard-tools, download the manual connection scripts from the PIA page linked here.'),
        t('Run run_setup.sh with VPN_PROTOCOL=wireguard and PIA_CONNECT=false, plus your PIA_USER, PIA_PASS, a PREFERRED_REGION and a PIA_CONF_PATH. It writes the file instead of connecting.'),
        t('Open the file it wrote and paste it below.'),
      ],
      notes: [
        t('You type your PIA login on your own computer, never into Mediarium.'),
        t('PIA hands out the address and key when you run the script. If the connection stops working later, run it again and add the new file.'),
      ],
      dns: [],
      presharedKey: false,
    },
    {
      id: 'protonvpn',
      name: 'ProtonVPN',
      link: 'https://protonvpn.com/support/wireguard-configurations',
      linkIsGuide: true,
      steps: [
        t('Sign in at account.protonvpn.com and open Downloads, then WireGuard configuration.'),
        t('Give the config a name, then choose a platform, your options and a server.'),
        t('Press Create, then Download.'),
      ],
      notes: [],
      dns: [],
      presharedKey: false,
    },
    {
      id: 'strongvpn',
      name: 'StrongVPN',
      link: 'https://wg.strongvpn.com',
      linkIsGuide: false,
      steps: [
        t('Go to wg.strongvpn.com and sign in with your StrongVPN account email and password.'),
        t('Choose your system and a location, then press Generate.'),
        t('Download the file.'),
      ],
      notes: [t('Each file is for one device. StrongVPN keeps 12 of them, and making more deletes the oldest, which stops working.')],
      dns: [],
      presharedKey: false,
    },
    {
      id: 'surfshark',
      name: 'Surfshark',
      link: 'https://support.surfshark.com/hc/en-us/articles/19262406140306',
      linkIsGuide: true,
      steps: [
        t('Log in to Surfshark and open VPN, then Manual setup, then Desktop or mobile, then WireGuard.'),
        t("Choose I don't have a key pair, give it a name and press Generate a new key pair. Save the private key, because it is only shown once."),
        t('Press Choose a location, pick a server and download the file.'),
      ],
      notes: [t('Make the key pair before you download. A file downloaded before that has no keys in it.')],
      dns: ['162.252.172.57', '149.154.159.92'],
      presharedKey: false,
    },
    {
      id: 'torguard',
      name: 'TorGuard',
      link: 'https://www.torguard.net/config-generator',
      linkIsGuide: false,
      steps: [
        t('Sign in to your TorGuard account and open Config Generator.'),
        t('Choose WireGuard as the tunnel type and pick a location.'),
        t('Enter your VPN username, press Generate Config and save the result.'),
      ],
      notes: [t("TorGuard's help pages don't show an example file, so check each field below after you paste.")],
      dns: [],
      presharedKey: false,
    },
    {
      id: 'windscribe',
      name: 'Windscribe',
      link: 'https://windscribe.com/getconfig/wireguard',
      linkIsGuide: false,
      steps: [
        t('Log in to Windscribe, open My Account, then the Config Generator tab, then WireGuard.'),
        t("Pick a Location and a Port (443 if you're not sure). Keep Generate new keypair."),
        t('Press Download Config and paste the whole file, preshared key included.'),
      ],
      notes: [t('WireGuard files are for paid accounts only.')],
      dns: [],
      presharedKey: true,
    },
    {
      id: 'selfhosted',
      name: t('Your own WireGuard server'),
      link: 'https://docs.pivpn.io/wireguard/',
      linkIsGuide: true,
      steps: [
        t("Make a new client in your server's tool. With PiVPN that is the command pivpn add."),
        t("Open the client's .conf file. PiVPN keeps it in the configs folder inside your home folder."),
        t('Paste it below. Its Endpoint has to be an address Mediarium can reach.'),
      ],
      notes: [t('Your traffic leaves from wherever the server is. A server at home still shows your home address.')],
      dns: [],
      presharedKey: false,
    },
    {
      id: OTHER_PROVIDER,
      name: t('Not listed / any other provider'),
      link: '',
      linkIsGuide: false,
      steps: [
        t('Get a WireGuard config file (.conf) from your provider. Look for WireGuard under manual setup, config generator or devices.'),
        t('Paste it below, or choose the file.'),
      ],
      notes: [t('Some providers only offer WireGuard inside their own app and give no file. Those can\'t be used here.')],
      dns: [],
      presharedKey: false,
    },
  ]
}

// The name shown for a stored connection. Connections saved before a provider
// was on the list keep whatever name they were saved with.
export function providerName(id: string): string {
  return vpnProviders().find((p) => p.id === id)?.name ?? id
}

// What looks off about a pasted file for the chosen provider, in plain words.
// Nothing here blocks adding the connection; the fields below stay editable.
export function confHints(provider: VpnProvider, conf: WireGuardConf): string[] {
  const out: string[] = []
  if (!conf.privateKey) out.push(t('The file has no private key line, so add the private key in the field below.'))
  if (provider.presharedKey && !conf.presharedKey) {
    out.push(t('{name} files normally have a preshared key, and this one has none. Without it the tunnel never connects. Download the file again and paste all of it.', { name: provider.name }))
  }
  return out
}
