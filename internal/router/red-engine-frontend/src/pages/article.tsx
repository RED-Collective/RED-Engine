import { useEffect, useState } from 'react';
import { Link, useSearchParams } from 'react-router';
import {
  FaCheckCircle,
  FaExclamationTriangle,
  FaFileAlt,
  FaFolder,
  FaChevronRight,
} from 'react-icons/fa';

type RecentlyEditedItem = {
  title: string;
  path: string;
  verification_state: string;
};

type NavNode = {
  id: number;
  path: string;
  display_name: string;
  is_leaf: boolean;
  is_guide?: boolean;
  child_count: number;
  guide_count: number;
  children?: NavNode[];
};

function VerificationBadge({ state }: { state: string }) {
  const isVerified = state === 'verified';
  return (
    <span
      className={`inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-body font-semibold ${
        isVerified
          ? 'bg-green-100 text-green-700'
          : 'bg-amber-100 text-amber-700'
      }`}
    >
      {isVerified ? <FaCheckCircle /> : <FaExclamationTriangle />}
      {isVerified ? 'Verified' : 'Unsigned'}
    </span>
  );
}

function DirBreadcrumbs({ dir }: { dir: string }) {
  const parts = dir.split('/').filter(Boolean);
  const crumbs = parts.map((part, i) => ({
    label: part,
    path: parts.slice(0, i + 1).join('/'),
  }));

  return (
    <nav className="flex items-center gap-1 text-sm font-body text-neutral-500 mb-6">
      <Link
        to={{ pathname: '/articles' }}
        className="hover:text-brand-600 transition-colors"
      >
        All vaults
      </Link>
      {crumbs.map((c, i) => (
        <span key={i} className="flex items-center gap-1">
          <FaChevronRight className="text-[10px] text-neutral-300" />
          {i < crumbs.length - 1 ? (
            <Link
              to={{ pathname: '/articles', search: `?dir=${encodeURIComponent(c.path)}` }}
              className="hover:text-brand-600 transition-colors"
            >
              {c.label}
            </Link>
          ) : (
            <span className="text-neutral-700 font-medium">{c.label}</span>
          )}
        </span>
      ))}
    </nav>
  );
}

export default function Articles() {
  const [searchParams] = useSearchParams();
  const dir = searchParams.get('dir');

  const [recentArticles, setRecentArticles] = useState<RecentlyEditedItem[]>([]);
  const [dirNode, setDirNode] = useState<NavNode | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (dir) {
      setLoading(true);
      async function fetchDir() {
        try {
          const res = await fetch(`/api/navigation?path=${encodeURIComponent(dir!)}`);
          if (!res.ok) throw new Error('Failed to fetch directory');
          const data = await res.json();
          setDirNode(data);
        } catch (err) {
          console.error(err);
        } finally {
          setLoading(false);
        }
      }
      fetchDir();
    } else {
      setLoading(true);
      async function fetchRecent() {
        try {
          const res = await fetch('/api/recent-files');
          if (!res.ok) throw new Error('Failed to fetch');
          const data = await res.json();
          setRecentArticles(data);
        } catch (err) {
          console.error(err);
        } finally {
          setLoading(false);
        }
      }
      fetchRecent();
    }
  }, [dir]);

  if (loading) {
    return (
      <div className="flex-1 p-8 max-w-4xl mx-auto w-full">
        <div className="animate-pulse flex flex-col gap-4">
          {[1, 2, 3].map((i) => (
            <div key={i} className="h-16 bg-brand-100 rounded-md" />
          ))}
        </div>
      </div>
    );
  }

  if (dir && dirNode) {
    const folders = (dirNode.children ?? []).filter((c) => !c.is_guide);
    const guides = (dirNode.children ?? []).filter((c) => c.is_guide);

    return (
      <div className="flex-1 p-8 max-w-4xl mx-auto w-full">
        <DirBreadcrumbs dir={dir} />
        <h1 className="text-3xl font-black text-neutral-900 mb-1">
          {dirNode.display_name}
        </h1>
        <p className="text-neutral-500 font-body text-sm mb-6">
          {dirNode.guide_count} article{dirNode.guide_count !== 1 ? 's' : ''}
          {dirNode.child_count > 0 && ` across ${dirNode.child_count} subfolder${dirNode.child_count !== 1 ? 's' : ''}`}
        </p>

        {folders.length === 0 && guides.length === 0 ? (
          <div className="text-center py-16 text-neutral-400">
            <FaFolder className="mx-auto text-5xl mb-4" />
            <p className="font-body text-lg">This vault is empty</p>
          </div>
        ) : (
          <div className="flex flex-col gap-3">
            {folders.map((folder) => (
              <Link
                key={folder.id}
                to={{ pathname: '/articles', search: `?dir=${encodeURIComponent(folder.path)}` }}
                className="group flex items-center gap-4 bg-white border border-brand-100 rounded-lg p-4 hover:border-brand-300 hover:shadow-sm transition-all duration-200"
              >
                <div className="shrink-0 w-10 h-10 bg-amber-50 rounded-lg flex items-center justify-center text-amber-500 group-hover:bg-amber-100 transition-colors">
                  <FaFolder />
                </div>
                <div className="flex-1 min-w-0">
                  <h2 className="text-base font-semibold text-neutral-900">
                    {folder.display_name}
                  </h2>
                  <p className="text-xs font-body text-neutral-400 mt-0.5">
                    {folder.guide_count} article{folder.guide_count !== 1 ? 's' : ''}
                    {folder.child_count > 0 && `, ${folder.child_count} subfolder${folder.child_count !== 1 ? 's' : ''}`}
                  </p>
                </div>
              </Link>
            ))}
            {guides.map((guide) => (
              <Link
                key={guide.id}
                to={{ pathname: '/article', search: `?path=${encodeURIComponent(guide.path)}` }}
                className="group flex items-center gap-4 bg-white border border-brand-100 rounded-lg p-4 hover:border-brand-300 hover:shadow-sm transition-all duration-200"
              >
                <div className="shrink-0 w-10 h-10 bg-brand-50 rounded-lg flex items-center justify-center text-brand-500 group-hover:bg-brand-200 transition-colors">
                  <FaFileAlt />
                </div>
                <div className="flex-1 min-w-0">
                  <h2 className="text-base font-semibold text-neutral-900">
                    {guide.display_name}
                  </h2>
                </div>
              </Link>
            ))}
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="flex-1 p-8 max-w-4xl mx-auto w-full">
      <div className="mb-8">
        <h1 className="text-3xl font-black text-neutral-900">Articles</h1>
        <p className="text-neutral-500 font-body text-sm mt-1">
          Recently edited documents on the network
        </p>
      </div>

      {recentArticles.length === 0 ? (
        <div className="text-center py-20 text-neutral-400">
          <FaFileAlt className="mx-auto text-5xl mb-4" />
          <p className="font-body text-lg">No articles found</p>
        </div>
      ) : (
        <div className="flex flex-col gap-4">
          {recentArticles.map((article) => (
            <Link
              key={article.path}
              to={{ pathname: '/article', search: `?path=${encodeURIComponent(article.path)}` }}
              className="group flex items-center gap-5 bg-white border border-brand-100 rounded-lg p-5 hover:border-brand-300 hover:shadow-sm transition-all duration-200"
            >
              <div className="shrink-0 w-10 h-10 bg-brand-100 rounded-lg flex items-center justify-center text-brand-500 group-hover:bg-brand-200 transition-colors">
                <FaFileAlt />
              </div>
              <div className="flex-1 min-w-0">
                <h2 className="text-lg font-semibold text-neutral-900 truncate">
                  {article.title}
                </h2>
                <p className="text-sm font-body text-neutral-400 truncate mt-0.5">
                  {article.path}
                </p>
              </div>
              <VerificationBadge state={article.verification_state} />
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
