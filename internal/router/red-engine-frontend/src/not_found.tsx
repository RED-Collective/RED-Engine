import { Link } from 'react-router';

export default function NotFound() {
  return (
    <div className="min-h-screen text-center align-middle justify-center gap-16 flex flex-col font-mono overflow-hidden">
      <img
        className="fixed inset-0 min-h-screen min-w-screen -z-10 opacity-70 sepia-50 grayscale -mt-32"
        src="/images/Cole_Thomas_The_Course_of_Empire_Destruction_1836.jpg"
      />
      <div className="flex flex-col gap-8">
        <p className="italic text-3xl">
          We're sorry! This page does not exist.
        </p>
        <p className="font-black text-8xl">404 Not Found</p>
      </div>
      <div>
        <Link
          className="text-4xl text-brand-400 bg-brand-950 p-4 rounded-2xl"
          to={{
            pathname: '/',
          }}
        >
          Return Home
        </Link>
      </div>
    </div>
  );
}
