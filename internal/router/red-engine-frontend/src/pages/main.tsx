export default function MainPage() {
  return (
    <div className="flex-1 flex relative">
      {/* Background image */}
      <div className="bg-[url('images/school_of_athens.jpg')] bg-[center_60%] sepia-[0.6] opacity-60 absolute inset-0 bg-cover bg-no-repeat" />
      {/* Gradient overlay to bottom */}
      <div className="absolute inset-0 bg-linear-to-b from-transparent via-transparent to-tertiary-50" />

      <div className="flex flex-1 flex-col items-center z-10">
        <h2 className="text-center mt-20 text-4xl font-body font-light tracking-widest uppercase text-brand-400">
          RED Engine Protocol
        </h2>

        <div className="mt-6">
          <h1 className="font-black text-neutral-900 text-7xl text-center leading-tight">
            Resilient, Encrypted, Decentralized
          </h1>
          <h1 className="text-center text-7xl font-black text-brand-500">
            Knowledge
          </h1>
        </div>

        <div className="mt-10 h-[3px] w-80 bg-linear-to-r from-transparent via-brand-600 to-transparent" />
        <p className="italic text-2xl font-body mt-24 text-brand-800/80 text-center max-w-2xl">
          "Reclaiming the intellectual commons through secure decentralization."
        </p>
      </div>
    </div>
  );
}
