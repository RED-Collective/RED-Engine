import { useEffect, useState } from 'react';

type RecentlyEditedItem = {
    title: string;
    path: string;
    verification_state: string; // Either unsigned
};

export default function Articles() {
    const [recentArticles, setRecentArticles] = useState([]);

    useEffect(() => {
        async function fetchArticles() {
            const res = await fetch('/api/recent-files');
            if (!res.ok) {
                throw new Error('Failed to fetch recent files');
            }

            const data = await res.json();

            setRecentArticles(data);
        }

        fetchArticles();
    }, []);

    return (
        <div className="flex flex-col gap-8">
            <div className="flex flex-col items-center gap-8 mt-8">
                <h1 className="text-3xl font-bold font-sans">
                    Recently Edited Articles
                </h1>
                <div className="flex flex-col items-left bg-brand-100 p-8 w-2xl rounded-sm text-black gap-4">
                    {/* List recently edited articles */}
                    {recentArticles.map((value: RecentlyEditedItem, index) => {
                        return (
                            <div
                                className="flex flex-col bg-secondary-600"
                                key={index + 'recentlyEdited'}
                            >
                                <h2>{value.title}</h2>
                                <p
                                    className={`ml-8 ${value.verification_state === 'verified'}`}
                                >
                                    {value.verification_state}
                                </p>
                            </div>
                        );
                    })}
                </div>
            </div>
        </div>
    );
}
