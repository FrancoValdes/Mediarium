import CleanupCard from '../../components/CleanupCard'
import ServerStatsCard from '../../components/ServerStats'
import { RestartCard } from '../../components/UpdateCards'

// The server itself: how it is doing, restarting and clean-up. Updates, the
// address you open Mediarium at and the legal notice are on General, help for a
// support request is on Logs and errors, and the script that runs after an
// import is under Library.
export default function SystemSettings() {
  return (
    <div className="settings-stack">
      <ServerStatsCard />
      <RestartCard />
      <CleanupCard />
    </div>
  )
}
