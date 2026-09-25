import { Link } from 'react-router'
import { link } from '../lib/ui'

export default function NotFound() {
  return (
    <div className="py-16 text-center">
      <h1 className="text-lg font-semibold">Page not found</h1>
      <p className="mt-2 text-sm text-zinc-500">
        <Link to="/stacks" className={link}>
          Back to stacks
        </Link>
      </p>
    </div>
  )
}
