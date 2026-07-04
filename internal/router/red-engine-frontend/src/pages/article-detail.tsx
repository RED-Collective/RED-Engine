import { useEffect, useState } from 'react';
import { useSearchParams, Link } from 'react-router';
import {
  FaCheckCircle,
  FaExclamationTriangle,
  FaTimesCircle,
  FaArrowLeft,
  FaArrowRight,
  FaFileAlt,
} from 'react-icons/fa';

type ArticleContent = {
  title: string;
  body_html: string;
  verification_state: string;
  verification_error?: string;
  signer_key?: string;
  author?: string;
  signed_at?: string;
  hash: string;
  tags?: string[];
  crumb: { label: string; path: string }[];
  prev_article?: { title: string; path: string } | null;
  next_article?: { title: string; path: string } | null;
  is_directory: boolean;
  backlinks: { file_path: string; title: string; kind: string }[];
};

function VerificationBadge({ state }: { state: string }) {
  if (state === 'verified') {
    return (
      <span className="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-body font-semibold bg-green-100 text-green-700">
        <FaCheckCircle /> Verified
      </span>
    );
  }
  if (state === 'unsigned') {
    return (
      <span className="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-body font-semibold bg-amber-100 text-amber-700">
        <FaExclamationTriangle /> Unsigned
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-body font-semibold bg-red-100 text-red-700">
      <FaTimesCircle /> {state}
    </span>
  );
}

export default function ArticleDetail() {
  const [searchParams] = useSearchParams();
  const path = searchParams.get('path');
  const [article, setArticle] = useState<ArticleContent | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!path) {
      setLoading(false);
      return;
    }
    async function fetchContent() {
      try { 
        const res = await fetch(`/api/content?path=${encodeURIComponent(path!)}`);
        if (!res.ok) throw new Error('Failed to fetch article');
        const data = await res.json();
        setArticle(data);
      } catch (err) {
        console.error(err);
      } finally {
        setLoading(false);
      }
    }
    fetchContent();
  }, [path]);

  if (!path) {
    return (
      <div className="flex-1 flex flex-col items-center justify-center text-neutral-400 gap-4">
        <FaFileAlt className="text-5xl" />
        <p className="font-body text-lg">No article specified</p>
        <Link to={{ pathname: '/articles' }} className="text-brand-500 hover:underline font-body">
          Browse articles
        </Link>
      </div>
    );
  }

  if (loading) {
    return (
      <div className="flex-1 p-8 max-w-3xl mx-auto w-full">
        <div className="animate-pulse flex flex-col gap-4">
          <div className="h-8 w-64 bg-brand-100 rounded" />
          <div className="h-4 w-48 bg-brand-100 rounded" />
          <div className="h-96 bg-brand-100 rounded mt-4" />
        </div>
      </div>
    );
  }

  if (!article) {
    return (
      <div className="flex-1 flex flex-col items-center justify-center text-neutral-400 gap-4">
        <FaFileAlt className="text-5xl" />
        <p className="font-body text-lg">Article not found</p>
        <Link to={{ pathname: '/articles' }} className="text-brand-500 hover:underline font-body">
          Browse articles
        </Link>
      </div>
    );
  }

  return (
    <div className="flex-1 p-8 max-w-3xl mx-auto w-full">
      {/* Breadcrumbs */}
      {article.crumb.length > 0 && (
        <nav className="flex flex-wrap items-center gap-1 text-sm font-body text-neutral-400 mb-6">
          <Link
            to={{ pathname: '/articles' }}
            className="hover:text-brand-600 transition-colors"
          >
            All vaults
          </Link>
          {article.crumb.map((c, i) => (
            <span key={i} className="flex items-center gap-1">
              <span className="text-neutral-300 mx-1">/</span>
              {i < article.crumb.length - 1 ? (
                <Link
                  to={{ pathname: '/articles', search: `?dir=${encodeURIComponent(c.path.replace(/^\//, ''))}` }}
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
      )}

      {/* Header */}
      <div className="mb-8">
        <div className="flex items-start gap-4">
          <h1 className="text-4xl font-black text-neutral-900 flex-1">
            {article.title}
          </h1>
          <VerificationBadge state={article.verification_state} />
        </div>
        {article.author && (
          <p className="font-body text-sm text-neutral-500 mt-2">
            By {article.author}
            {article.signed_at && <> &middot; {article.signed_at}</>}
          </p>
        )}
        {article.tags && article.tags.length > 0 && (
          <div className="flex flex-wrap gap-2 mt-3">
            {article.tags.map((tag) => (
              <span
                key={tag}
                className="px-2.5 py-0.5 rounded-full text-xs font-body font-medium bg-brand-50 text-brand-600"
              >
                #{tag}
              </span>
            ))}
          </div>
        )}
      </div>

      {/* Body */}
      <div
        className="prose prose-neutral max-w-none font-body"
        dangerouslySetInnerHTML={{ __html: article.body_html }}
      />

      {/* Prev / Next Navigation */}
      <div className="flex justify-between mt-12 pt-8 border-t border-brand-100">
        <div>
          {article.prev_article && (
            <Link
              to={{
                pathname: '/article',
                search: `?path=${encodeURIComponent(article.prev_article.path)}`,
              }}
              className="group flex items-center gap-2 text-neutral-500 hover:text-brand-600 transition-colors"
            >
              <FaArrowLeft className="text-sm" />
              <span className="font-body text-sm group-hover:underline">
                {article.prev_article.title}
              </span>
            </Link>
          )}
        </div>
        <div>
          {article.next_article && (
            <Link
              to={{
                pathname: '/article',
                search: `?path=${encodeURIComponent(article.next_article.path)}`,
              }}
              className="group flex items-center gap-2 text-neutral-500 hover:text-brand-600 transition-colors"
            >
              <span className="font-body text-sm group-hover:underline">
                {article.next_article.title}
              </span>
              <FaArrowRight className="text-sm" />
            </Link>
          )}
        </div>
      </div>

      {/* Backlinks */}
      {article.backlinks.length > 0 && (
        <div className="mt-8 pt-6 border-t border-brand-100">
          <h3 className="text-sm font-body font-semibold uppercase tracking-widest text-neutral-400 mb-3">
            Linked from
          </h3>
          <div className="flex flex-col gap-2">
            {article.backlinks.map((bl, i) => (
              <Link
                key={i}
                to={{
                  pathname: '/article',
                  search: `?path=${encodeURIComponent(bl.file_path)}`,
                }}
                className="font-body text-sm text-brand-600 hover:underline"
              >
                {bl.title || bl.file_path}
              </Link>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
