// This is the main page for the node

export default function MainPage() {
  return (
    <div className="flex-1 flex bg-cover bg-center bg-no-repeat relative">
      {/* Background div */}
      <div className="bg-[url('images/school_of_athens.jpg')] sepia-40 opacity-55  absolute inset-0 bg-cover bg-center bg-no-repeat z-0"></div>
      {/* Opacity gradient to bottom */}
      <div className="absolute inset-0 bg-linear-to-b from-transparent to-white" />

      <div className="flex flex-1 flex-col justify-items-center align-middle">
        <h1 className="m-auto justify-center z-10 font-black text-neutral-900 text-8xl">
          Decentralized, Stateless, Verifiable, Knowledge
        </h1>
      </div>
    </div>
  );
}
