// Shown by the service worker with no server to ask, so the words are here.
var words = {
  es: ["No se puede llegar a Mediarium", "Este dispositivo no tiene conexión con tu Mediarium ahora mismo. Comprueba que está en la misma red (o que tu VPN está activada) y que el servidor está en marcha.", "No se pierde nada: lo que hacías sigue en el servidor.", "Reintentar"],
  de: ["Mediarium ist nicht erreichbar", "Dieses Gerät hat gerade keine Verbindung zu deinem Mediarium. Prüfe, ob es im selben Netzwerk ist (oder dein VPN an ist) und ob der Server läuft.", "Es geht nichts verloren: Was du gemacht hast, ist weiter auf dem Server.", "Erneut versuchen"],
  fr: ["Mediarium est injoignable", "Cet appareil n’a pas de connexion à votre Mediarium en ce moment. Vérifiez qu’il est sur le même réseau (ou que votre VPN est activé) et que le serveur tourne.", "Rien n’est perdu : ce que vous faisiez est toujours sur le serveur.", "Réessayer"],
  pt: ["Não foi possível acessar o Mediarium", "Este dispositivo está sem conexão com o seu Mediarium agora. Veja se ele está na mesma rede (ou com a VPN ligada) e se o servidor está rodando.", "Nada se perde: o que você estava fazendo continua no servidor.", "Tentar de novo"],
  "pt-pt": ["Não foi possível aceder ao Mediarium", "Este dispositivo está sem ligação ao seu Mediarium neste momento. Veja se está na mesma rede (ou com a VPN ligada) e se o servidor está a funcionar.", "Não se perde nada: o que estava a fazer continua no servidor.", "Tentar de novo"],
  it: ["Mediarium non è raggiungibile", "Questo dispositivo non è collegato al tuo Mediarium in questo momento. Controlla che sia sulla stessa rete (o che la VPN sia attiva) e che il server sia acceso.", "Non si perde niente: quello che stavi facendo è ancora sul server.", "Riprova"],
  nl: ["Mediarium is niet bereikbaar", "Dit apparaat heeft nu geen verbinding met je Mediarium. Kijk of het op hetzelfde netwerk zit (of je VPN aan staat) en of de server draait.", "Er gaat niets verloren: waar je mee bezig was, staat nog op de server.", "Opnieuw proberen"],
  pl: ["Nie można połączyć się z Mediarium", "To urządzenie nie ma teraz połączenia z Twoim Mediarium. Sprawdź, czy jest w tej samej sieci (albo czy VPN jest włączony) i czy serwer działa.", "Nic nie przepadło: to, co robiłeś, nadal jest na serwerze.", "Spróbuj ponownie"],
  ru: ["Mediarium недоступен", "Это устройство сейчас не подключено к вашему Mediarium. Проверьте, что оно в той же сети (или что VPN включён) и что сервер работает.", "Ничего не потеряно: всё, что вы делали, осталось на сервере.", "Повторить"],
  uk: ["Mediarium недоступний", "Цей пристрій зараз не має з’єднання з вашим Mediarium. Перевірте, чи він у тій самій мережі (або чи увімкнено VPN) і чи працює сервер.", "Нічого не втрачено: те, що ви робили, залишилося на сервері.", "Спробувати ще раз"]
};
// The language chosen in Mediarium (same address, so the same storage), else the browser's.
var chosen = null;
try {
  chosen = localStorage.getItem("mediarium-lang");
} catch {
  // storage blocked: use the browser's language
}
var lang = (chosen || navigator.language || "en").toLowerCase();
var w = words[lang] || words[lang.split("-")[0]];
if (w) {
  document.documentElement.lang = lang.split("-")[0];
  document.title = w[0];
  document.getElementById("h").textContent = w[0];
  document.getElementById("a").textContent = w[1];
  document.getElementById("b").textContent = w[2];
  document.getElementById("r").textContent = w[3];
}
document.getElementById("r").addEventListener("click", function () { location.reload(); });
